package handler

import (
	"context"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeRunner records what syncBoxBranch hands the SSH transport so we can assert
// the orchestration without a real connection.
type fakeRunner struct {
	host, user, key, script string
	port                    int
	out                     string
	err                     error
}

func (f *fakeRunner) Run(_ context.Context, host, user string, port int, keyPath, script string) (string, error) {
	f.host, f.user, f.port, f.key, f.script = host, user, port, keyPath, script
	return f.out, f.err
}

// TestSyncBoxBranch verifies the orchestration wires the box's SSH target + the
// built git-sync script to the runner, with the github token injected.
func TestSyncBoxBranch(t *testing.T) {
	box := db.ConnectedBox{
		SshHost: "193.149.18.99",
		SshUser: "jamshidfr",
		SshPort: 33022,
		RepoUrl: "https://github.com/azizkh/sd.git",
		WorkDir: "/var/www/agora-test.sdteam.uz",
	}
	fr := &fakeRunner{out: "Switched to a new branch"}
	out, err := syncBoxBranch(context.Background(), box, "btx-32077", "ghtok", "/keys/deploy", fr)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out != "Switched to a new branch" {
		t.Errorf("output not passed through: %q", out)
	}
	if fr.host != box.SshHost || fr.user != box.SshUser || fr.port != 33022 || fr.key != "/keys/deploy" {
		t.Errorf("ssh target wrong: %+v", fr)
	}
	for _, want := range []string{
		"/var/www/agora-test.sdteam.uz",
		"x-access-token:ghtok@github.com/azizkh/sd.git",
		"git checkout -B 'btx-32077'",
	} {
		if !strings.Contains(fr.script, want) {
			t.Errorf("script missing %q\nscript: %s", want, fr.script)
		}
	}
}

// TestAuthedRepoURL covers token injection for private-repo fetch over https.
func TestAuthedRepoURL(t *testing.T) {
	const base = "https://gitlab.sdteam.uz/salesdoctor/sd-bridge/repo.git"

	if got := authedRepoURL(base, "tok123"); got != "https://oauth2:tok123@gitlab.sdteam.uz/salesdoctor/sd-bridge/repo.git" {
		t.Errorf("gitlab token not injected correctly: %q", got)
	}
	// GitHub uses x-access-token, not oauth2.
	if got := authedRepoURL("https://github.com/azizkh/sd.git", "ghtok"); got != "https://x-access-token:ghtok@github.com/azizkh/sd.git" {
		t.Errorf("github token format wrong: %q", got)
	}
	if got := authedRepoURL(base, ""); got != base {
		t.Errorf("empty token must leave URL unchanged: %q", got)
	}
	if got := authedRepoURL(base, "  "); got != base {
		t.Errorf("blank token must leave URL unchanged: %q", got)
	}
	// ssh-style URL is not rewritten.
	const sshURL = "git@gitlab.sdteam.uz:salesdoctor/repo.git"
	if got := authedRepoURL(sshURL, "tok"); got != sshURL {
		t.Errorf("ssh URL must be unchanged: %q", got)
	}
	// An already-tokenized URL must not double-inject userinfo.
	pre := "https://oauth2:old@gitlab.sdteam.uz/x/repo.git"
	if got := authedRepoURL(pre, "new"); got != "https://oauth2:new@gitlab.sdteam.uz/x/repo.git" {
		t.Errorf("must replace existing userinfo, not stack it: %q", got)
	}
}

// TestBuildGitSyncScript pins the sync command: clone-if-absent, fetch the
// pushed branch, hard-checkout it, never install anything.
func TestBuildGitSyncScript(t *testing.T) {
	script := buildGitSyncScript(gitSyncParams{
		WorkDir: "/home/dev/agora/repo",
		RepoURL: "https://gitlab.sdteam.uz/g/repo.git",
		Branch:  "cocode/SD-175",
		Token:   "tok123",
	})

	for _, want := range []string{
		"set -e",
		"if [ ! -d '/home/dev/agora/repo'/.git ]; then git clone",
		"oauth2:tok123@gitlab.sdteam.uz/g/repo.git",
		"git fetch --prune origin 'cocode/SD-175'",
		"git checkout -B 'cocode/SD-175' --track origin/'cocode/SD-175'",
		"git reset --hard origin/'cocode/SD-175'",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("sync script missing %q\nscript: %s", want, script)
		}
	}
	// Hard constraint: the sync NEVER installs anything.
	for _, banned := range []string{"apt", "npm install", "pip", "curl ", "wget ", "install"} {
		if strings.Contains(script, banned) {
			t.Errorf("sync script must not install anything; found %q", banned)
		}
	}
}

// TestRedactGitToken keeps the token out of logs.
func TestRedactGitToken(t *testing.T) {
	in := "git clone https://oauth2:supersecret@gitlab.sdteam.uz/g/repo.git /dir && cd /dir"
	out := redactGitToken(in)
	if strings.Contains(out, "supersecret") {
		t.Errorf("token leaked: %q", out)
	}
	if !strings.Contains(out, "oauth2:***@gitlab.sdteam.uz") {
		t.Errorf("expected redacted marker, got: %q", out)
	}
	// Multiple occurrences all redacted (set-url repeats the URL).
	multi := "a oauth2:t1@h/x b oauth2:t2@h/y"
	if strings.Contains(redactGitToken(multi), "t1") || strings.Contains(redactGitToken(multi), "t2") {
		t.Errorf("not all tokens redacted: %q", redactGitToken(multi))
	}
}

// TestShellQuote defends against a branch/path breaking out of the command.
func TestShellQuote(t *testing.T) {
	if got := shellQuote("a'b"); got != `'a'\''b'` {
		t.Errorf("single-quote escaping wrong: %q", got)
	}
	// A malicious branch name stays inside one quoted token.
	script := buildGitSyncScript(gitSyncParams{
		WorkDir: "/d", RepoURL: "https://h/r.git", Branch: "x'; rm -rf /; echo '",
	})
	if strings.Contains(script, "; rm -rf /;") && !strings.Contains(script, `'\''`) {
		t.Errorf("branch injection not neutralized: %s", script)
	}
}
