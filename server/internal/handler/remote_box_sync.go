package handler

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Remote Boxes git-sync (runner-delivers model). The real dev/QA boxes are
// locked down — git pull + checkout only, no installs — AND can't reach the
// private git host with the box user's own creds. So Agora does NOT make the box
// clone: the RUNNER (this host, which already has git host access for the agent)
// shallow-clones the branch, then streams its file tree to the box over SSH
// (`git archive | ssh box tar -x`). The box receives a tar and needs no git, no
// host creds, no token. The branch's code lands in a directory the box already
// serves (e.g. an nginx PHP site), so QA can hit its URL. Proven live against a
// real box. The command-construction is pure + unit-tested; the exec/pipe
// transport is integration-tested against a box.

// authedRepoURL injects a token into an https git URL so the RUNNER can clone a
// private repo non-interactively. The username is host-specific: GitHub wants
// `x-access-token:<token>@`, GitLab wants `oauth2:<token>@`. Empty token returns
// the URL unchanged; non-https (ssh-style) URLs are returned as-is.
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
	if at := strings.Index(rest, "@"); at >= 0 && !strings.Contains(rest[:at], "/") {
		rest = rest[at+1:] // drop any existing userinfo so we don't double-inject
	}
	user := "oauth2"
	if strings.HasPrefix(strings.ToLower(rest), "github.com") {
		user = "x-access-token"
	}
	return httpsPrefix + user + ":" + token + "@" + rest
}

// buildRunnerCloneArgs is the `git` argv the RUNNER uses to fetch just the one
// branch (shallow, single-branch — cheap even for a many-branch repo).
func buildRunnerCloneArgs(repoURL, token, branch, dir string) []string {
	return []string{
		"clone", "--depth", "1", "--single-branch", "--branch", branch,
		authedRepoURL(repoURL, token), dir,
	}
}

// buildBoxReceiveScript is the remote /bin/sh command the box runs to receive the
// streamed tar: ensure the dir, clear its contents, extract. It uses ONLY `tar`
// (already present) — never installs anything, honoring the box's locked-down
// constraint. `find -mindepth 1 -delete` clears hidden files too without
// removing the served directory itself.
func buildBoxReceiveScript(workDir string) string {
	d := shellQuote(workDir)
	return fmt.Sprintf("set -e; mkdir -p %s; find %s -mindepth 1 -delete; tar -x -C %s", d, d, d)
}

// sshArgs builds the ssh argv the control plane uses to reach a box. The control
// plane always initiates the connection (the box never dials out).
func sshArgs(host, user string, port int, keyPath, remoteCmd string) []string {
	return []string{
		"-i", keyPath,
		"-p", strconv.Itoa(port),
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
		user + "@" + host,
		remoteCmd,
	}
}

// delivery is the seam the sync orchestration runs through — a fake records the
// calls in unit tests; realDelivery execs git + ssh.
type delivery interface {
	// CloneBranch shallow-clones branch of repoURL into dir on the RUNNER.
	CloneBranch(ctx context.Context, repoURL, token, branch, dir string) error
	// DeliverTree streams `git archive` of srcDir to the box's workDir over SSH.
	DeliverTree(ctx context.Context, srcDir, host, user string, port int, keyPath, workDir string) error
}

type realDelivery struct{}

func (realDelivery) CloneBranch(ctx context.Context, repoURL, token, branch, dir string) error {
	out, err := exec.CommandContext(ctx, "git", buildRunnerCloneArgs(repoURL, token, branch, dir)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("clone failed: %s: %w", redactGitToken(string(out)), err)
	}
	return nil
}

func (realDelivery) DeliverTree(ctx context.Context, srcDir, host, user string, port int, keyPath, workDir string) error {
	archive := exec.CommandContext(ctx, "git", "-C", srcDir, "archive", "--format=tar", "HEAD")
	ssh := exec.CommandContext(ctx, "ssh", sshArgs(host, user, port, keyPath, buildBoxReceiveScript(workDir))...)

	pipe, err := archive.StdoutPipe()
	if err != nil {
		return err
	}
	ssh.Stdin = pipe
	var sshOut bytes.Buffer
	ssh.Stdout = &sshOut
	ssh.Stderr = &sshOut

	if err := ssh.Start(); err != nil { // start the reader first so archive never blocks on a full pipe
		return err
	}
	if err := archive.Run(); err != nil { // writes the tar, then closes the pipe (signals EOF to tar)
		_ = ssh.Wait()
		return fmt.Errorf("archive failed: %w", err)
	}
	if err := ssh.Wait(); err != nil {
		return fmt.Errorf("deliver failed: %s: %w", strings.TrimSpace(sshOut.String()), err)
	}
	return nil
}

// syncBoxBranch checks a branch out onto the box: the runner clones it into
// tmpDir, then streams the tree to box.WorkDir. The box ends up serving exactly
// that branch's code, with no git host access of its own.
func syncBoxBranch(ctx context.Context, box db.ConnectedBox, branch, token, keyPath, tmpDir string, d delivery) error {
	if err := d.CloneBranch(ctx, box.RepoUrl, token, branch, tmpDir); err != nil {
		return err
	}
	return d.DeliverTree(ctx, tmpDir, box.SshHost, box.SshUser, int(box.SshPort), keyPath, box.WorkDir)
}

// redactGitToken removes an injected git token from a string so it is safe to
// log. Replaces `<user>:<token>@` with `<user>:***@` for the prefixes we inject
// (oauth2 for GitLab, x-access-token for GitHub).
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

// remoteBoxesSSHKeyPath / remoteBoxesGitToken are operator-provided secrets for
// the Remote Boxes control plane (v1): the deploy private key the runner SSHes
// to the box with, and the git token the runner uses to clone the private repo.
// Per-box encrypted storage is a later hardening step. The token lives on the
// RUNNER only — never on the locked-down box.
func remoteBoxesSSHKeyPath() string { return strings.TrimSpace(os.Getenv("AGORA_REMOTE_BOXES_SSH_KEY")) }
func remoteBoxesGitToken() string   { return strings.TrimSpace(os.Getenv("AGORA_REMOTE_BOXES_GIT_TOKEN")) }

// shellQuote single-quotes a string for safe embedding in a remote /bin/sh
// command (defends a path/branch with spaces or shell metacharacters from
// breaking or injecting into the command).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
