package handler

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// remoteRunner runs a shell script on a box over SSH. It's an interface so the
// sync orchestration is unit-testable with a fake; the production impl execs the
// `ssh` binary with the control-plane's deploy key.
type remoteRunner interface {
	Run(ctx context.Context, host, user string, port int, keyPath, script string) (string, error)
}

// sshRunner is the real SSH transport. The control plane initiates the
// connection (box never dials out) using the per-deployment deploy key.
type sshRunner struct{}

func (sshRunner) Run(ctx context.Context, host, user string, port int, keyPath, script string) (string, error) {
	args := []string{
		"-i", keyPath,
		"-p", strconv.Itoa(port),
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
		user + "@" + host,
		script,
	}
	out, err := exec.CommandContext(ctx, "ssh", args...).CombinedOutput()
	return string(out), err
}

// remoteBoxesSSHKeyPath / remoteBoxesGitToken are operator-provided secrets for
// the Remote Boxes control plane (v1 dogfood): the path to the deploy private
// key the backend SSHes with, and the git token injected into the fetch URL for
// the private repo. Per-box encrypted storage is a later hardening step.
func remoteBoxesSSHKeyPath() string { return strings.TrimSpace(os.Getenv("AGORA_REMOTE_BOXES_SSH_KEY")) }
func remoteBoxesGitToken() string   { return strings.TrimSpace(os.Getenv("AGORA_REMOTE_BOXES_GIT_TOKEN")) }

// syncBoxBranch checks out `branch` of the box's configured repo into its
// work_dir over SSH (clone-if-absent + fetch + hard checkout), so the box serves
// that branch. Returns the remote output (token already redacted by the caller
// before logging). Pure orchestration around buildGitSyncScript + a runner.
func syncBoxBranch(ctx context.Context, box db.ConnectedBox, branch, token, keyPath string, runner remoteRunner) (string, error) {
	script := buildGitSyncScript(gitSyncParams{
		WorkDir: box.WorkDir,
		RepoURL: box.RepoUrl,
		Branch:  branch,
		Token:   token,
	})
	return runner.Run(ctx, box.SshHost, box.SshUser, int(box.SshPort), keyPath, script)
}

// Remote Boxes git-sync (the locked-down-box model). A connected box can only
// pull code and change branches — nothing may be installed on it — so the agent
// runs on Agora's own runner and pushes a branch to the git host; Agora then
// SSHes to the dev's box and checks that branch out, so the box reflects the
// agent's work. This file builds the remote shell command that performs that
// sync. It is PURE (no SSH / no DB) so the exact command — and its token
// handling — is unit-testable; the SSH transport that runs it is layered on top.

// gitSyncParams is everything needed to render a sync command for one box.
type gitSyncParams struct {
	WorkDir string // absolute path on the box to hold the checkout, e.g. /home/dev/agora/repo
	RepoURL string // https clone URL, e.g. https://gitlab.sdteam.uz/group/repo.git
	Branch  string // the branch the agent pushed
	Token   string // optional git token for a private repo (injected as oauth2:<token>@)
}

// authedRepoURL injects a token into an https git URL so a private repo can be
// cloned/fetched non-interactively. The username part is host-specific: GitHub
// wants `x-access-token:<token>@`, GitLab wants `oauth2:<token>@`. Empty token
// (public repo, or creds already on the box) returns the URL unchanged. Only
// https URLs are rewritten — an ssh-style URL is returned as-is.
func authedRepoURL(repoURL, token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return repoURL
	}
	const httpsPrefix = "https://"
	if !strings.HasPrefix(repoURL, httpsPrefix) {
		return repoURL
	}
	rest := strings.TrimPrefix(repoURL, httpsPrefix)
	// Drop any userinfo already present so we don't double-inject.
	if at := strings.Index(rest, "@"); at >= 0 && !strings.Contains(rest[:at], "/") {
		rest = rest[at+1:]
	}
	user := "oauth2"
	if host := rest; strings.HasPrefix(strings.ToLower(host), "github.com") {
		user = "x-access-token"
	}
	return httpsPrefix + user + ":" + token + "@" + rest
}

// buildGitSyncScript renders the idempotent remote shell command Agora runs over
// SSH on the box. First run clones; later runs fetch + hard-reset onto the
// pushed branch. It NEVER installs anything — only `git` (already present) is
// used — honoring the box's git-pull-and-branch-only constraint. `set -e` aborts
// on the first failed step so a partial sync surfaces as a non-zero exit.
func buildGitSyncScript(p gitSyncParams) string {
	dir := shellQuote(p.WorkDir)
	url := shellQuote(authedRepoURL(p.RepoURL, p.Token))
	branch := shellQuote(p.Branch)
	// If the dir is not yet a git repo, clone it; then always re-point origin (in
	// case the token rotated), fetch the branch, and hard-checkout it.
	return strings.Join([]string{
		"set -e",
		fmt.Sprintf("if [ ! -d %s/.git ]; then git clone %s %s; fi", dir, url, dir),
		fmt.Sprintf("cd %s", dir),
		fmt.Sprintf("git remote set-url origin %s", url),
		fmt.Sprintf("git fetch --prune origin %s", branch),
		fmt.Sprintf("git checkout -B %s --track origin/%s", branch, branch),
		fmt.Sprintf("git reset --hard origin/%s", branch),
	}, " && ")
}

// redactGitToken removes an injected git token from a string so it is safe to
// log. Replaces `<user>:<token>@` with `<user>:***@` for the userinfo prefixes
// we inject (oauth2 for GitLab, x-access-token for GitHub).
func redactGitToken(s string) string {
	for _, prefix := range []string{"oauth2:", "x-access-token:"} {
		s = redactUserToken(s, prefix)
	}
	return s
}

// redactUserToken replaces the secret in every `<prefix><secret>@` with `***`.
// It writes scanned input to a builder and advances PAST each match, so the
// inserted `***@` is never re-scanned — avoiding the infinite loop a naive
// in-place replace hits (the replacement still starts with the prefix).
func redactUserToken(s, prefix string) string {
	var b strings.Builder
	rest := s
	for {
		i := strings.Index(rest, prefix)
		if i < 0 {
			b.WriteString(rest)
			return b.String()
		}
		at := strings.Index(rest[i:], "@")
		if at < 0 {
			b.WriteString(rest)
			return b.String()
		}
		b.WriteString(rest[:i])
		b.WriteString(prefix)
		b.WriteString("***@")
		rest = rest[i+at+1:]
	}
}

// shellQuote single-quotes a string for safe embedding in a remote /bin/sh
// command (the values originate from operator/box config, but quoting keeps a
// path with spaces or a branch with shell metacharacters from breaking the
// command or injecting a second one).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
