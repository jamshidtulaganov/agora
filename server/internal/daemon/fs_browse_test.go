package daemon

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The folder picker walks the daemon owner's machine on behalf of a remote web
// user, so the gates below are the whole security story: a positive root gate
// (home + owner-approved dirs) plus a negative denylist for credential/OS
// stores. Listing must never approve anything.

// withHome points os.UserHomeDir at a temp dir for the duration of a test.
func withHome(t *testing.T, home string) {
	t.Helper()
	key := "HOME"
	if runtime.GOOS == "windows" {
		key = "USERPROFILE"
	}
	t.Setenv(key, home)
	if got, err := os.UserHomeDir(); err != nil || filepath.Clean(got) != filepath.Clean(home) {
		t.Skipf("cannot redirect home on this platform (got %q, err %v)", got, err)
	}
}

// isolateAllowlist keeps a test off the developer's real ~/.agora/local-dirs.json.
func isolateAllowlist(t *testing.T, entries string) {
	t.Helper()
	t.Setenv("AGORA_LOCAL_DIR_ALLOWLIST", entries)
}

func TestBrowseLocalDirListsOnlyDirectories(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)
	isolateAllowlist(t, "")

	mustMkdir(t, filepath.Join(home, "projects"))
	mustMkdir(t, filepath.Join(home, "code"))
	mustWrite(t, filepath.Join(home, "notes.txt"), "not a dir")

	res, status, err := browseLocalDir(home, false)
	if err != nil || status != http.StatusOK {
		t.Fatalf("browseLocalDir(home) = %d, %v", status, err)
	}
	names := entryNames(res.Entries)
	if len(names) != 2 || names[0] != "code" || names[1] != "projects" {
		t.Fatalf("expected sorted dirs [code projects], got %v", names)
	}
	if res.Path != filepath.Clean(home) {
		t.Errorf("Path = %q, want %q", res.Path, home)
	}
	// Home is a browsable root: the UI must not be offered a way above it.
	if res.Parent != "" {
		t.Errorf("Parent = %q, want blank at the home root boundary", res.Parent)
	}
}

// Empty path is the picker's landing view.
func TestBrowseLocalDirDefaultsToHome(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)
	isolateAllowlist(t, "")
	mustMkdir(t, filepath.Join(home, "work"))

	res, status, err := browseLocalDir("", false)
	if err != nil || status != http.StatusOK {
		t.Fatalf("browseLocalDir(\"\") = %d, %v", status, err)
	}
	if res.Path != filepath.Clean(home) {
		t.Fatalf("Path = %q, want home %q", res.Path, home)
	}
	if res.Home != filepath.Clean(home) {
		t.Errorf("Home = %q, want %q", res.Home, home)
	}
}

func TestBrowseLocalDirHiddenDirsAreOptIn(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)
	isolateAllowlist(t, "")
	mustMkdir(t, filepath.Join(home, "proj"))
	mustMkdir(t, filepath.Join(home, ".cache"))

	res, _, err := browseLocalDir(home, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := entryNames(res.Entries); len(got) != 1 || got[0] != "proj" {
		t.Fatalf("hidden dir leaked into default listing: %v", got)
	}
	// Even opted in, a dot-dir under $HOME stays unlistable (credential store
	// class) — surfacing the NAME is fine, walking INTO it is not.
	if _, status, err := browseLocalDir(filepath.Join(home, ".cache"), true); err == nil || status != http.StatusForbidden {
		t.Fatalf("expected 403 walking into a home dot-dir, got %d, %v", status, err)
	}
}

// A row the gates would 403 on click is worse than no row: the listing must
// only advertise folders that actually open.
func TestBrowseLocalDirOmitsUnenterableChildren(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)
	isolateAllowlist(t, "")
	mustMkdir(t, filepath.Join(home, ".ssh"))
	mustMkdir(t, filepath.Join(home, "proj"))
	if runtime.GOOS == "darwin" {
		mustMkdir(t, filepath.Join(home, "Library"))
	}

	// Hidden opted IN, so .ssh would otherwise survive the dotfile filter.
	res, _, err := browseLocalDir(home, true)
	if err != nil {
		t.Fatal(err)
	}
	names := entryNames(res.Entries)
	for _, n := range names {
		if n == ".ssh" || n == "Library" {
			t.Errorf("listed %q, but browsing into it is denied", n)
		}
	}
	if !contains(names, "proj") {
		t.Errorf("a normal project dir must still be listed, got %v", names)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func TestBrowseLocalDirFlagsGitRepos(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)
	isolateAllowlist(t, "")
	repo := filepath.Join(home, "repo")
	mustMkdir(t, filepath.Join(repo, ".git"))
	mustMkdir(t, filepath.Join(home, "plain"))

	res, _, err := browseLocalDir(home, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Entries {
		switch e.Name {
		case "repo":
			if !e.IsGitRepo {
				t.Error("repo should be flagged as a git repo")
			}
		case "plain":
			if e.IsGitRepo {
				t.Error("plain should not be flagged as a git repo")
			}
		}
	}
}

// The positive root gate is the one that stops full-disk enumeration; the
// pre-existing blacklist is rejection-only and would allow this.
func TestBrowseLocalDirRejectsOutsideBrowsableRoots(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)
	isolateAllowlist(t, "")
	outside := t.TempDir() // a real, readable dir that is simply not a root

	_, status, err := browseLocalDir(outside, false)
	if err == nil || status != http.StatusForbidden {
		t.Fatalf("expected 403 for a path outside home/approved, got %d, %v", status, err)
	}
}

// An owner-approved dir is browsable even outside home, and the walk is clamped
// to it — the approval is the consent that opens it.
func TestBrowseLocalDirAllowsApprovedRootAndClampsParent(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)
	root := t.TempDir()
	approved := filepath.Join(root, "code")
	mustMkdir(t, filepath.Join(approved, "app"))
	isolateAllowlist(t, approved)

	res, status, err := browseLocalDir(approved, false)
	if err != nil || status != http.StatusOK {
		t.Fatalf("approved root should be browsable: %d, %v", status, err)
	}
	if got := entryNames(res.Entries); len(got) != 1 || got[0] != "app" {
		t.Fatalf("entries = %v", got)
	}
	if res.Parent != "" {
		t.Errorf("Parent = %q, want blank at the approved-root boundary", res.Parent)
	}
	// A child of the approved root keeps the root as its parent.
	child, _, err := browseLocalDir(filepath.Join(approved, "app"), false)
	if err != nil {
		t.Fatal(err)
	}
	if child.Parent != filepath.Clean(approved) {
		t.Errorf("child Parent = %q, want %q", child.Parent, approved)
	}
	// The approved root's own parent stays closed.
	if _, status, err := browseLocalDir(root, false); err == nil || status != http.StatusForbidden {
		t.Fatalf("expected 403 above the approved root, got %d, %v", status, err)
	}
}

// A symlink out of home must not smuggle the walk somewhere the gates reject:
// the gate runs on the RESOLVED path.
func TestBrowseLocalDirRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	home := t.TempDir()
	withHome(t, home)
	isolateAllowlist(t, "")
	outside := t.TempDir()
	mustMkdir(t, filepath.Join(outside, "secret"))
	link := filepath.Join(home, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, status, err := browseLocalDir(link, false); err == nil || status != http.StatusForbidden {
		t.Fatalf("expected 403 through a symlink escaping home, got %d, %v", status, err)
	}
}

func TestBrowseLocalDirRejectsProtectedHomeSubtrees(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)
	isolateAllowlist(t, "")
	for _, name := range []string{".ssh", ".aws", ".config"} {
		mustMkdir(t, filepath.Join(home, name))
		if _, status, err := browseLocalDir(filepath.Join(home, name), true); err == nil || status != http.StatusForbidden {
			t.Errorf("expected 403 for ~/%s, got %d, %v", name, status, err)
		}
	}
}

func TestBrowseLocalDirRejectsSystemRoots(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)
	isolateAllowlist(t, "")
	for _, p := range []string{"/", "/etc"} {
		if _, status, err := browseLocalDir(p, false); err == nil || status != http.StatusForbidden {
			t.Errorf("expected 403 for %q, got %d, %v", p, status, err)
		}
	}
}

func TestBrowseLocalDirRejectsRelativeAndMissingAndFile(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)
	isolateAllowlist(t, "")
	mustWrite(t, filepath.Join(home, "file.txt"), "x")

	if _, status, err := browseLocalDir("relative/path", false); err == nil || status != http.StatusBadRequest {
		t.Errorf("relative path: got %d, %v; want 400", status, err)
	}
	if _, status, err := browseLocalDir(filepath.Join(home, "nope"), false); err == nil || status != http.StatusNotFound {
		t.Errorf("missing path: got %d, %v; want 404", status, err)
	}
	if _, status, err := browseLocalDir(filepath.Join(home, "file.txt"), false); err == nil || status != http.StatusBadRequest {
		t.Errorf("file path: got %d, %v; want 400", status, err)
	}
}

// Browsing is read-only: it must never record consent.
func TestBrowseLocalDirNeverApproves(t *testing.T) {
	home := t.TempDir()
	withHome(t, home)
	isolateAllowlist(t, "")
	proj := filepath.Join(home, "proj")
	mustMkdir(t, proj)

	if _, _, err := browseLocalDir(proj, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agora", "local-dirs.json")); !os.IsNotExist(err) {
		t.Fatalf("listing wrote an allowlist file (err=%v) — browsing must grant nothing", err)
	}
	// And the folder is still unapproved for execution.
	real, _ := resolveRealPath(proj)
	if isLocalDirApproved(real, nil) {
		t.Error("browsing must not make a folder approved")
	}
}

func entryNames(entries []fsBrowseEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
