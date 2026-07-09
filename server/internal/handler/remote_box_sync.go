package handler

import (
	"context"
	"fmt"
	"github.com/multica-ai/multica/server/internal/config"
	"os"
	"os/exec"
	"strconv"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Remote Boxes git-sync (box-fetches, glue-preserving model). The QA/dev boxes
// are locked down (git pull + checkout only, no installs). The agent runs on
// Agora's runner and pushes a branch to the git host; Agora then SSHes to the
// box, which fetches that branch and checks it out into the served directory, so
// the box serves — and QA can test — the right branch.
//
// WHY git CHECKOUT, not a tar overlay: the served sd-main site keeps per-site
// deployment glue (`index.php`, `protected/config/main.php` with the DB
// connection, `console.php`) that is GITIGNORED and created once per site. A
// `git checkout` updates only TRACKED files and leaves those untracked glue
// files untouched; a wipe-and-extract would destroy them and break the site on
// every sync. The box already serves the checkout (PHP, no build step).
//
// The git token is EPHEMERAL: it appears only in the `git fetch <url>` argv (and
// is redacted in logs) — never `set-url`'d into the box's stored `.git/config`,
// so a shared box never persists a credential. Command-construction is pure +
// unit-tested; the SSH transport is integration-tested against a box.

// authedRepoURL injects a token into an https git URL so the box can fetch a
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

// buildGitSyncScript renders the box-side /bin/sh command Agora runs over SSH:
// init the repo on first run (in-place, so existing untracked glue is kept),
// fetch the pushed branch with an ephemeral token, then force-checkout it. The
// `-f -B <branch> FETCH_HEAD` resets tracked files to the branch tip while
// leaving untracked deployment glue in place. It NEVER installs anything and
// NEVER wipes the directory — honoring the box's locked-down + glue constraints.
// origin is set to the TOKENLESS url; the token lives only in the fetch argv.
func buildGitSyncScript(workDir, repoURL, branch, token string) string {
	dir := shellQuote(workDir)
	tokenless := shellQuote(repoURL)
	authed := shellQuote(authedRepoURL(repoURL, token))
	br := shellQuote(branch)
	// GIT_CONFIG_* env is "protected configuration", the one place git still
	// honors safe.directory — a box dir owned by a different unix user than the
	// SSH login (e.g. www-data-owned /var/www/<dev> synced as the shared
	// control-plane user) otherwise fails every command with "dubious
	// ownership". Scoped to this script's own git invocations only.
	safeCfg := fmt.Sprintf("export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=safe.directory GIT_CONFIG_VALUE_0=%s", dir)
	return strings.Join([]string{
		"set -e",
		safeCfg,
		fmt.Sprintf("cd %s", dir),
		fmt.Sprintf("if [ ! -d .git ]; then git init -q && git remote add origin %s; fi", tokenless),
		// --depth 1: QA only needs the branch tip, and these repos can be large
		// (sd-main has 685 branches). A shallow single-branch fetch is cheap and
		// re-runs cleanly for any branch.
		fmt.Sprintf("git fetch --depth 1 %s %s", authed, br),
		fmt.Sprintf("git checkout -f -B %s FETCH_HEAD", br),
	}, " && ")
}

// sshArgs builds the ssh argv the control plane uses to reach a box. The control
// plane always initiates the connection (the box never dials out).
func sshArgs(host, user string, port int, keyPath, remoteCmd string) []string {
	return []string{
		"-i", keyPath,
		"-p", strconv.Itoa(port),
		"-o", "StrictHostKeyChecking=accept-new",
		// The backend container has no writable known_hosts; discard host keys
		// rather than fail to persist them. (The reachability channel is the
		// security boundary; host-key pinning is a hardening follow-up.)
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
		user + "@" + host,
		remoteCmd,
	}
}

// remoteRunner runs a shell script on a box over SSH — an interface so the sync
// orchestration is unit-testable with a fake; sshRunner execs the `ssh` binary
// with the control-plane deploy key.
type remoteRunner interface {
	Run(ctx context.Context, host, user string, port int, keyPath, script string) (string, error)
}

type sshRunner struct{}

func (sshRunner) Run(ctx context.Context, host, user string, port int, keyPath, script string) (string, error) {
	out, err := exec.CommandContext(ctx, "ssh", sshArgs(host, user, port, keyPath, script)...).CombinedOutput()
	return string(out), err
}

// syncBoxBranch checks a branch out onto the box (clone-on-first-run + fetch +
// glue-preserving checkout), so the box serves exactly that branch's code.
func syncBoxBranch(ctx context.Context, box db.ConnectedBox, branch, token, keyPath string, runner remoteRunner) (string, error) {
	script := buildGitSyncScript(box.WorkDir, box.RepoUrl, branch, token)
	return runner.Run(ctx, box.SshHost, box.SshUser, int(box.SshPort), keyPath, script)
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
// to the box with, and the git token injected into the box's fetch URL for the
// private repo. Per-box encrypted storage is a later hardening step.
func remoteBoxesSSHKeyPath() string {
	return strings.TrimSpace(os.Getenv("AGORA_REMOTE_BOXES_SSH_KEY"))
}
func remoteBoxesGitToken() string {
	return strings.TrimSpace(os.Getenv("AGORA_REMOTE_BOXES_GIT_TOKEN"))
}

// QA-host control plane (v1, operator env). The QA host is the SHARED parent
// server that per-developer QA boxes are carved out of as wildcard subdomains
// (`<handle>.<baseDomain>`, e.g. shakhzod.sdteam.uz). This config lives in env —
// NOT workspace.settings — because the parent SSH target + provisioning routing
// must not ride in GET /workspace (returned to every client); it mirrors the
// AGORA_REMOTE_BOXES_SSH_KEY / _GIT_TOKEN secrets above. The provisioner reuses
// the same deploy key + git token; the per-box DB clone uses the box's OWN local
// mysql auth, so no database password is ever an Agora-held secret.
func qaHostSSHHost() string    { return config.String("AGORA_QA_HOST_SSH_HOST") }
func qaHostSSHUser() string    { return config.String("AGORA_QA_HOST_SSH_USER") }
func qaHostBaseDomain() string { return config.String("AGORA_QA_HOST_BASE_DOMAIN") }
func qaHostWebRoot() string    { return config.String("AGORA_QA_HOST_WEB_ROOT") }
func qaHostRepoURL() string    { return strings.TrimSpace(os.Getenv("AGORA_QA_HOST_REPO_URL")) }
func qaHostSeedDir() string    { return strings.TrimSpace(os.Getenv("AGORA_QA_HOST_SEED_DIR")) }
func qaHostSeedDB() string     { return strings.TrimSpace(os.Getenv("AGORA_QA_HOST_SEED_DB")) }

// qaHostSSHPort is the parent host's SSH port (AGORA_QA_HOST_SSH_PORT); default 22.
func qaHostSSHPort() int {
	if v := strings.TrimSpace(os.Getenv("AGORA_QA_HOST_SSH_PORT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 22
}

// qaHostConfigured reports whether the QA-host control plane has the minimum
// config to provision a box: where to reach the host, where it serves from, what
// repo to check out, and a known-good seed site/DB to copy glue + data from.
// Provision returns 503 when this is false.
func qaHostConfigured() bool {
	return qaHostSSHHost() != "" && qaHostSSHUser() != "" && qaHostBaseDomain() != "" &&
		qaHostWebRoot() != "" && qaHostRepoURL() != "" && qaHostSeedDir() != "" && qaHostSeedDB() != ""
}

// qaHostAllowedTargetSuffix / qaHostRequireDBPrefix are OPT-IN, DEFAULT-OFF
// safety rails for non-prod (e.g. demo) deployments of this same provisioner.
// Both are "" unless a deployment explicitly sets them, so the real sd-main
// provisioning flow (AGORA_QA_HOST_SSH_HOST=agora.sdteam.uz, seed DB
// dbt_agora) is completely unaffected — see qaHostCheckTarget /
// qaHostCheckDBPrefix for why they exist.
func qaHostAllowedTargetSuffix() string {
	return strings.TrimSpace(os.Getenv("AGORA_QA_HOST_ALLOWED_TARGET_SUFFIX"))
}
func qaHostRequireDBPrefix() string {
	return strings.TrimSpace(os.Getenv("AGORA_QA_HOST_REQUIRE_DB_PREFIX"))
}

// qaHostCheckTarget is an opt-in guard (AGORA_QA_HOST_ALLOWED_TARGET_SUFFIX):
// this provisioner has NO host allowlist and copies the seed's db.php verbatim,
// so a misconfigured demo deployment (wrong/typo'd AGORA_QA_HOST_SSH_HOST or
// AGORA_QA_HOST_BASE_DOMAIN) can silently SSH into and mutate the REAL prod QA
// host (agora.sdteam.uz). When the env var is empty this is a no-op — current
// behavior for the real sd-main flow is byte-for-byte unchanged. When set, a
// demo deployment can pin it to e.g. "demo.sdteam.uz" so any resolved target
// that doesn't end with that suffix is refused before any SSH/mutation.
func qaHostCheckTarget(sshHost string, p provisionParams) error {
	return qaHostCheckTargetWithSuffix(qaHostAllowedTargetSuffix(), sshHost, p)
}

// qaHostCheckTargetWithSuffix is the pure core of qaHostCheckTarget (suffix
// passed explicitly, not read from env), so it can be unit-tested without
// mutating process env. Compares case-insensitively (hostnames/domains are
// not case sensitive).
func qaHostCheckTargetWithSuffix(suffix, sshHost string, p provisionParams) error {
	if suffix == "" {
		return nil
	}
	suffixLower := strings.ToLower(suffix)
	targets := map[string]string{
		"SSH host":      sshHost,
		"base domain":   p.BaseDomain,
		"box subdomain": boxSubdomain(p),
	}
	for label, val := range targets {
		if !strings.HasSuffix(strings.ToLower(val), suffixLower) {
			return fmt.Errorf("refusing to provision: %s %q does not end with the allowed target suffix %q (AGORA_QA_HOST_ALLOWED_TARGET_SUFFIX)", label, val, suffix)
		}
	}
	return nil
}

// qaHostCheckDBPrefix is an opt-in guard (AGORA_QA_HOST_REQUIRE_DB_PREFIX):
// this provisioner copies the seed site's protected/config/db.php VERBATIM
// (see buildProvisionScript) — it never reads or validates the DB name that
// file actually binds to, because that value only exists on the remote host.
// The only LOCAL signal we have is AGORA_QA_HOST_SEED_DB (the operator-declared
// seed DB, reported back to the caller for review). When the env var is empty
// this is a no-op — current behavior is unchanged. When set, a demo deployment
// can require e.g. "demo_" so a seed DB name that isn't clearly non-prod is
// refused before any SSH/mutation.
//
// RESIDUAL GAP: this does NOT prove the seed's remote db.php binds to a
// demo_-prefixed database — only that the operator's declared AGORA_QA_HOST_SEED_DB
// value matches. If AGORA_QA_HOST_SEED_DB is misconfigured (or the remote
// db.php has drifted from it), this check cannot catch that; closing that gap
// needs a remote SSH read of the seed's db.php, which is intentionally out of
// scope for this pass.
func qaHostCheckDBPrefix(seedDB string) error {
	return qaHostCheckDBPrefixWithPrefix(qaHostRequireDBPrefix(), seedDB)
}

// qaHostCheckDBPrefixWithPrefix is the pure core of qaHostCheckDBPrefix
// (prefix passed explicitly, not read from env), so it can be unit-tested
// without mutating process env. The prefix compare is case-sensitive — unlike
// hostnames, DB names are.
func qaHostCheckDBPrefixWithPrefix(prefix, seedDB string) error {
	if prefix == "" {
		return nil
	}
	if !strings.HasPrefix(seedDB, prefix) {
		return fmt.Errorf("refusing to provision: seed DB %q does not start with the required prefix %q (AGORA_QA_HOST_REQUIRE_DB_PREFIX)", seedDB, prefix)
	}
	return nil
}

// shellQuote single-quotes a string for safe embedding in a remote /bin/sh
// command (defends a path/branch with spaces or shell metacharacters from
// breaking or injecting into the command).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
