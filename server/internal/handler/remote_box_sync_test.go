package handler

import (
	"context"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestAuthedRepoURL covers token injection for the box's private-repo fetch.
func TestAuthedRepoURL(t *testing.T) {
	const gl = "https://gitlab.sdteam.uz/salesdoctor/sd-bridge/repo.git"
	if got := authedRepoURL(gl, "tok123"); got != "https://oauth2:tok123@gitlab.sdteam.uz/salesdoctor/sd-bridge/repo.git" {
		t.Errorf("gitlab token format wrong: %q", got)
	}
	if got := authedRepoURL("https://github.com/jamshidtulaganov/sd-main.git", "ghtok"); got != "https://x-access-token:ghtok@github.com/jamshidtulaganov/sd-main.git" {
		t.Errorf("github token format wrong: %q", got)
	}
	if got := authedRepoURL(gl, ""); got != gl {
		t.Errorf("empty token must leave URL unchanged: %q", got)
	}
	if got := authedRepoURL(gl, "  "); got != gl {
		t.Errorf("blank token must leave URL unchanged: %q", got)
	}
	if ssh := "git@github.com:jamshidtulaganov/sd-main.git"; authedRepoURL(ssh, "tok") != ssh {
		t.Error("ssh URL must be unchanged")
	}
	if got := authedRepoURL("https://oauth2:old@gitlab.sdteam.uz/x/repo.git", "new"); got != "https://oauth2:new@gitlab.sdteam.uz/x/repo.git" {
		t.Errorf("must replace existing userinfo, not stack it: %q", got)
	}
}

// TestBuildGitSyncScript pins the glue-preserving box-fetches sync: in-place init
// (never clone-into / wipe), token only in the fetch, force-checkout FETCH_HEAD.
func TestBuildGitSyncScript(t *testing.T) {
	script := buildGitSyncScript(
		"/var/www/agora.sdteam.uz",
		"https://github.com/jamshidtulaganov/sd-main.git",
		"baxodir/btx-32077",
		"ghtok",
	)
	for _, want := range []string{
		"cd '/var/www/agora.sdteam.uz'",
		"if [ ! -d .git ]; then git init -q && git remote add origin 'https://github.com/jamshidtulaganov/sd-main.git'",
		"git fetch --prune 'https://x-access-token:ghtok@github.com/jamshidtulaganov/sd-main.git' 'baxodir/btx-32077'",
		"git checkout -f -B 'baxodir/btx-32077' FETCH_HEAD",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("sync script missing %q\nscript: %s", want, script)
		}
	}
	// Glue-preservation: must NOT wipe the dir or clone-into it (would destroy the
	// gitignored deployment glue) and must NEVER install anything.
	for _, banned := range []string{"git clone", "rm -rf", "find ", "-delete", "git remote set-url", "apt", "npm install", "install"} {
		if strings.Contains(script, banned) {
			t.Errorf("sync script must not contain %q (glue / locked-down violation)\nscript: %s", banned, script)
		}
	}
	// The token must appear ONLY in the fetch line, never in `remote add origin`.
	originLine := ""
	for _, ln := range strings.Split(script, " && ") {
		if strings.Contains(ln, "remote add origin") {
			originLine = ln
		}
	}
	if strings.Contains(originLine, "ghtok") {
		t.Errorf("token leaked into the persisted origin: %q", originLine)
	}
}

// fakeRunner records what syncBoxBranch hands the SSH transport.
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
// built (glue-preserving) script to the runner, with the github token injected.
func TestSyncBoxBranch(t *testing.T) {
	box := db.ConnectedBox{
		SshHost: "193.149.18.99",
		SshUser: "jamshidfr",
		SshPort: 33022,
		RepoUrl: "https://github.com/jamshidtulaganov/sd-main.git",
		WorkDir: "/var/www/agora.sdteam.uz",
	}
	fr := &fakeRunner{out: "Switched to a new branch"}
	out, err := syncBoxBranch(context.Background(), box, "baxodir/btx-32077", "ghtok", "/keys/deploy", fr)
	if err != nil || out != "Switched to a new branch" {
		t.Fatalf("unexpected result: out=%q err=%v", out, err)
	}
	if fr.host != box.SshHost || fr.user != box.SshUser || fr.port != 33022 || fr.key != "/keys/deploy" {
		t.Errorf("ssh target wrong: %+v", fr)
	}
	for _, want := range []string{
		"/var/www/agora.sdteam.uz",
		"x-access-token:ghtok@github.com/jamshidtulaganov/sd-main.git",
		"git checkout -f -B 'baxodir/btx-32077' FETCH_HEAD",
	} {
		if !strings.Contains(fr.script, want) {
			t.Errorf("script missing %q\nscript: %s", want, fr.script)
		}
	}
}

// TestRedactGitToken keeps the token out of logs without infinite-looping (the
// bug the suite caught earlier via timeout).
func TestRedactGitToken(t *testing.T) {
	in := "git fetch https://x-access-token:supersecret@github.com/j/sd-main.git br"
	out := redactGitToken(in)
	if strings.Contains(out, "supersecret") {
		t.Errorf("token leaked: %q", out)
	}
	if !strings.Contains(out, "x-access-token:***@github.com") {
		t.Errorf("expected redacted marker, got: %q", out)
	}
	multi := "oauth2:t1@h/x x-access-token:t2@h/y"
	if r := redactGitToken(multi); strings.Contains(r, "t1") || strings.Contains(r, "t2") {
		t.Errorf("not all tokens redacted: %q", r)
	}
}

// TestShellQuote defends against a path/branch breaking out of the box command.
func TestShellQuote(t *testing.T) {
	if got := shellQuote("a'b"); got != `'a'\''b'` {
		t.Errorf("single-quote escaping wrong: %q", got)
	}
	// A malicious branch name stays inside one quoted token.
	s := buildGitSyncScript("/d", "https://h/r.git", "x'; rm -rf /; echo '", "")
	if strings.Contains(s, "; rm -rf /;") && !strings.Contains(s, `'\''`) {
		t.Errorf("branch injection not neutralized: %s", s)
	}
}
