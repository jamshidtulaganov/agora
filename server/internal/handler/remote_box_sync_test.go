package handler

import (
	"context"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestAuthedRepoURL covers token injection for the RUNNER's private-repo clone.
func TestAuthedRepoURL(t *testing.T) {
	const gl = "https://gitlab.sdteam.uz/salesdoctor/sd-bridge/repo.git"
	if got := authedRepoURL(gl, "tok123"); got != "https://oauth2:tok123@gitlab.sdteam.uz/salesdoctor/sd-bridge/repo.git" {
		t.Errorf("gitlab token format wrong: %q", got)
	}
	if got := authedRepoURL("https://github.com/azizkh/sd.git", "ghtok"); got != "https://x-access-token:ghtok@github.com/azizkh/sd.git" {
		t.Errorf("github token format wrong: %q", got)
	}
	if got := authedRepoURL(gl, ""); got != gl {
		t.Errorf("empty token must leave URL unchanged: %q", got)
	}
	if got := authedRepoURL(gl, "  "); got != gl {
		t.Errorf("blank token must leave URL unchanged: %q", got)
	}
	if ssh := "git@gitlab.sdteam.uz:salesdoctor/repo.git"; authedRepoURL(ssh, "tok") != ssh {
		t.Error("ssh URL must be unchanged")
	}
	// Already-tokenized URL must not double-inject userinfo.
	if got := authedRepoURL("https://oauth2:old@gitlab.sdteam.uz/x/repo.git", "new"); got != "https://oauth2:new@gitlab.sdteam.uz/x/repo.git" {
		t.Errorf("must replace existing userinfo, not stack it: %q", got)
	}
}

// TestBuildRunnerCloneArgs pins the runner-side clone: shallow, single-branch,
// token-injected.
func TestBuildRunnerCloneArgs(t *testing.T) {
	args := buildRunnerCloneArgs("https://github.com/jamshidtulaganov/sd-main.git", "ghtok", "baxodir/btx-32077", "/tmp/x")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"clone --depth 1 --single-branch --branch baxodir/btx-32077",
		"https://x-access-token:ghtok@github.com/jamshidtulaganov/sd-main.git",
		"/tmp/x",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("clone args missing %q: %v", want, args)
		}
	}
}

// TestBuildBoxReceiveScript pins the box-side receive: it only uses tar (never
// installs), clears the dir (incl. hidden), and extracts.
func TestBuildBoxReceiveScript(t *testing.T) {
	s := buildBoxReceiveScript("/var/www/agora.sdteam.uz")
	for _, want := range []string{
		"mkdir -p '/var/www/agora.sdteam.uz'",
		"find '/var/www/agora.sdteam.uz' -mindepth 1 -delete",
		"tar -x -C '/var/www/agora.sdteam.uz'",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("receive script missing %q: %s", want, s)
		}
	}
	for _, banned := range []string{"apt", "npm install", "pip", "git clone", "install", "curl ", "wget "} {
		if strings.Contains(s, banned) {
			t.Errorf("receive script must not install / fetch anything; found %q", banned)
		}
	}
}

// fakeDelivery records the orchestration's calls.
type fakeDelivery struct {
	clonedRepo, clonedToken, clonedBranch, clonedDir string
	dHost, dUser, dKey, dWork                        string
	dPort                                            int
	cloneErr                                         error
}

func (f *fakeDelivery) CloneBranch(_ context.Context, repoURL, token, branch, dir string) error {
	f.clonedRepo, f.clonedToken, f.clonedBranch, f.clonedDir = repoURL, token, branch, dir
	return f.cloneErr
}
func (f *fakeDelivery) DeliverTree(_ context.Context, srcDir, host, user string, port int, keyPath, workDir string) error {
	f.dHost, f.dUser, f.dPort, f.dKey, f.dWork = host, user, port, keyPath, workDir
	if srcDir != f.clonedDir {
		return nil
	}
	return nil
}

// TestSyncBoxBranch verifies the runner-delivers orchestration: clone the box's
// repo branch into the temp dir, then deliver that dir to the box's SSH target +
// work_dir.
func TestSyncBoxBranch(t *testing.T) {
	box := db.ConnectedBox{
		SshHost: "193.149.18.99",
		SshUser: "jamshidfr",
		SshPort: 33022,
		RepoUrl: "https://github.com/jamshidtulaganov/sd-main.git",
		WorkDir: "/var/www/agora.sdteam.uz",
	}
	fd := &fakeDelivery{}
	if err := syncBoxBranch(context.Background(), box, "baxodir/btx-32077", "ghtok", "/keys/deploy", "/tmp/work", fd); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if fd.clonedRepo != box.RepoUrl || fd.clonedToken != "ghtok" || fd.clonedBranch != "baxodir/btx-32077" || fd.clonedDir != "/tmp/work" {
		t.Errorf("clone call wrong: %+v", fd)
	}
	if fd.dHost != box.SshHost || fd.dUser != box.SshUser || fd.dPort != 33022 || fd.dKey != "/keys/deploy" || fd.dWork != box.WorkDir {
		t.Errorf("deliver call wrong: %+v", fd)
	}
}

// TestRedactGitToken keeps the token out of logs and does NOT infinite-loop (the
// bug the suite caught earlier via timeout).
func TestRedactGitToken(t *testing.T) {
	in := "git clone https://x-access-token:supersecret@github.com/j/sd-main.git /dir"
	out := redactGitToken(in)
	if strings.Contains(out, "supersecret") {
		t.Errorf("token leaked: %q", out)
	}
	if !strings.Contains(out, "x-access-token:***@github.com") {
		t.Errorf("expected redacted marker, got: %q", out)
	}
	// Multiple occurrences (gitlab + github) all redacted, no hang.
	multi := "oauth2:t1@h/x x-access-token:t2@h/y"
	r := redactGitToken(multi)
	if strings.Contains(r, "t1") || strings.Contains(r, "t2") {
		t.Errorf("not all tokens redacted: %q", r)
	}
}

// TestShellQuote defends against a path/branch breaking out of the box command.
func TestShellQuote(t *testing.T) {
	if got := shellQuote("a'b"); got != `'a'\''b'` {
		t.Errorf("single-quote escaping wrong: %q", got)
	}
	// A malicious work_dir stays inside one quoted token in the receive script.
	s := buildBoxReceiveScript("/d'; rm -rf /; echo '")
	if strings.Contains(s, "; rm -rf /;") && !strings.Contains(s, `'\''`) {
		t.Errorf("injection not neutralized: %s", s)
	}
}
