package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setTestHome points os.UserHomeDir at a temp dir for the duration of the
// test. HOME covers unix; USERPROFILE covers windows.
func setTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func writeAllowlistFile(t *testing.T, home string, dirs []string) string {
	t.Helper()
	dir := filepath.Join(home, ".agora")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(localDirAllowlistFile{Version: 1, Dirs: dirs})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, localDirsFileName)
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestIsLocalDirApproved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("path fixtures are POSIX-shaped")
	}
	// The daemon always passes the symlink-RESOLVED task path (RealPath), so
	// resolve the fixture the same way (macOS tempdirs live under /var ->
	// /private/var). The allowlist ENTRY stays in whatever form the user
	// recorded — isLocalDirApproved resolves entries itself.
	rawProj := t.TempDir()
	proj, err := filepath.EvalSymlinks(rawProj)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		path    string
		entries []string
		want    bool
	}{
		{"exact match", proj, []string{proj}, true},
		{"entry in unresolved form still matches", proj, []string{rawProj}, true},
		{"nested child", filepath.Join(proj, "sub", "deep"), []string{proj}, true},
		{"sibling shared prefix rejected", proj + "-other", []string{proj}, false},
		{"no entries", proj, nil, false},
		{"unrelated entry", proj, []string{"/somewhere/else"}, false},
		{"second entry matches", proj, []string{"/somewhere/else", proj}, true},
		{"parent not approved by child", filepath.Dir(proj), []string{proj}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLocalDirApproved(tc.path, tc.entries); got != tc.want {
				t.Errorf("isLocalDirApproved(%q, %v) = %v, want %v", tc.path, tc.entries, got, tc.want)
			}
		})
	}
}

func TestIsLocalDirApprovedResolvesEntrySymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	real := t.TempDir()
	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "proj-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	// Approval recorded via the symlink must match the resolved task path —
	// the daemon compares on RealPath.
	realResolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if !isLocalDirApproved(realResolved, []string{link}) {
		t.Errorf("symlinked approval entry %q should approve resolved path %q", link, realResolved)
	}
}

func TestLoadLocalDirAllowlistMergesFileAndEnv(t *testing.T) {
	home := setTestHome(t)
	writeAllowlistFile(t, home, []string{"/from/file"})
	t.Setenv("AGORA_LOCAL_DIR_ALLOWLIST", "/from/env-a"+string(os.PathListSeparator)+" /from/env-b ")

	entries, err := loadLocalDirAllowlist()
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}
	want := map[string]bool{"/from/file": true, "/from/env-a": true, "/from/env-b": true}
	if len(entries) != len(want) {
		t.Fatalf("entries = %v, want the %d merged paths", entries, len(want))
	}
	for _, e := range entries {
		if !want[e] {
			t.Errorf("unexpected entry %q", e)
		}
	}
}

func TestLoadLocalDirAllowlistMissingFileIsEmpty(t *testing.T) {
	setTestHome(t)
	entries, err := loadLocalDirAllowlist()
	if err != nil {
		t.Fatalf("missing file must not error, got %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %v, want empty", entries)
	}
}

func TestLoadLocalDirAllowlistCorruptFileFailsClosedButKeepsEnv(t *testing.T) {
	home := setTestHome(t)
	dir := filepath.Join(home, ".agora")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, localDirsFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGORA_LOCAL_DIR_ALLOWLIST", "/env/survives")

	entries, err := loadLocalDirAllowlist()
	if err == nil {
		t.Fatal("corrupt file should surface a load error")
	}
	if len(entries) != 1 || entries[0] != "/env/survives" {
		t.Fatalf("entries = %v, want just the env entry", entries)
	}
}

func TestCheckLocalDirApproved(t *testing.T) {
	home := setTestHome(t)
	proj := t.TempDir()
	real, err := filepath.EvalSymlinks(proj)
	if err != nil {
		t.Fatal(err)
	}

	// Unapproved: error must name both approval paths so the fail comment
	// is self-healing.
	err = checkLocalDirApproved(proj, real)
	if err == nil {
		t.Fatal("unapproved path must be rejected")
	}
	for _, needle := range []string{"allow-dir", "desktop", "AGORA_LOCAL_DIR_ALLOWLIST"} {
		if !strings.Contains(err.Error(), needle) {
			t.Errorf("deny message should mention %q, got: %s", needle, err.Error())
		}
	}

	writeAllowlistFile(t, home, []string{proj})
	if err := checkLocalDirApproved(proj, real); err != nil {
		t.Fatalf("approved path rejected: %v", err)
	}
}

func TestApproveLocalDir(t *testing.T) {
	home := setTestHome(t)
	proj := t.TempDir()

	added, file, err := ApproveLocalDir(proj)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !added {
		t.Fatal("first approve should report added=true")
	}
	if want := filepath.Join(home, ".agora", localDirsFileName); file != want {
		t.Errorf("file = %q, want %q", file, want)
	}

	// Idempotent.
	added, _, err = ApproveLocalDir(proj)
	if err != nil {
		t.Fatalf("re-approve: %v", err)
	}
	if added {
		t.Error("second approve should report added=false")
	}

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var f localDirAllowlistFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("written file is not valid json: %v", err)
	}
	if len(f.Dirs) != 1 || f.Dirs[0] != filepath.Clean(proj) {
		t.Errorf("dirs = %v, want [%s]", f.Dirs, filepath.Clean(proj))
	}
	if f.Version != 1 {
		t.Errorf("version = %d, want 1", f.Version)
	}
}

func TestApproveLocalDirRejectsProtectedPaths(t *testing.T) {
	home := setTestHome(t)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApproveLocalDir(sshDir); err == nil {
		t.Error("approving ~/.ssh must fail")
	}
	if _, _, err := ApproveLocalDir(home); err == nil {
		t.Error("approving $HOME must fail")
	}
	if _, _, err := ApproveLocalDir("relative/path"); err == nil {
		t.Error("relative path must fail")
	}
	if _, _, err := ApproveLocalDir(filepath.Join(home, "does-not-exist")); err == nil {
		t.Error("missing dir must fail")
	}
}

func TestRevokeLocalDir(t *testing.T) {
	home := setTestHome(t)
	a := t.TempDir()
	b := t.TempDir()
	if _, _, err := ApproveLocalDir(a); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApproveLocalDir(b); err != nil {
		t.Fatal(err)
	}

	// Revoking a present path removes only it.
	removed, _, err := RevokeLocalDir(a)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !removed {
		t.Fatal("expected removed=true")
	}
	entries, _ := loadLocalDirAllowlist()
	if len(entries) != 1 || filepath.Clean(entries[0]) != filepath.Clean(b) {
		t.Fatalf("entries = %v, want just %s", entries, b)
	}

	// Revoking an absent path is a no-op, not an error.
	removed, _, err = RevokeLocalDir(a)
	if err != nil || removed {
		t.Fatalf("re-revoke should be a no-op: removed=%v err=%v", removed, err)
	}

	// Revoking when the file doesn't exist yet is a no-op.
	_ = home
	other := setTestHome(t)
	_ = other
	if removed, _, err := RevokeLocalDir(a); err != nil || removed {
		t.Fatalf("revoke with no file should be a no-op: removed=%v err=%v", removed, err)
	}
}

func TestListLocalDirs(t *testing.T) {
	setTestHome(t)
	proj := t.TempDir()
	if _, _, err := ApproveLocalDir(proj); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGORA_LOCAL_DIR_ALLOWLIST", "/env/only")

	fileDirs, envDirs, _, err := ListLocalDirs()
	if err != nil {
		t.Fatal(err)
	}
	if len(fileDirs) != 1 || filepath.Clean(fileDirs[0]) != filepath.Clean(proj) {
		t.Errorf("fileDirs = %v, want [%s]", fileDirs, proj)
	}
	if len(envDirs) != 1 || envDirs[0] != "/env/only" {
		t.Errorf("envDirs = %v, want [/env/only]", envDirs)
	}
}

func TestApproveLocalDirRepairsCorruptFile(t *testing.T) {
	home := setTestHome(t)
	dir := filepath.Join(home, ".agora")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, localDirsFileName), []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	added, file, err := ApproveLocalDir(proj)
	if err != nil {
		t.Fatalf("approve over corrupt file: %v", err)
	}
	if !added {
		t.Fatal("expected added=true")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var f localDirAllowlistFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("file not repaired to valid json: %v", err)
	}
}

func TestIsProtectedHomeSubtree(t *testing.T) {
	home := "/Users/dev"
	cases := []struct {
		path    string
		blocked bool
	}{
		{"/Users/dev/.ssh", true},
		{"/Users/dev/.ssh/keys", true},
		{"/Users/dev/.aws", true},
		{"/Users/dev/.config/deep/nested", true},
		{"/Users/dev/projects/app", false},
		{"/Users/dev/code", false},
		// Boundary: a dir merely sharing the ".ssh" prefix is not hidden.
		{"/Users/dev/ssh-notes", false},
		{"/elsewhere/.ssh", false},
		{"/Users/dev", false}, // home itself handled by the caller
	}
	for _, tc := range cases {
		_, blocked := isProtectedHomeSubtree(filepath.Clean(tc.path), filepath.Clean(home))
		if blocked != tc.blocked {
			t.Errorf("isProtectedHomeSubtree(%q) = %v, want %v", tc.path, blocked, tc.blocked)
		}
	}
	if runtime.GOOS == "darwin" {
		if _, blocked := isProtectedHomeSubtree("/Users/dev/Library/Keychains", "/Users/dev"); !blocked {
			t.Error("~/Library must be blocked on darwin")
		}
	}
}

func TestValidateLocalPathRejectsProtectedHomeSubtrees(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture")
	}
	home := setTestHome(t)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateLocalPath(sshDir); err == nil {
		t.Error("validateLocalPath(~/.ssh) must fail")
	}

	// Symlink from a legit-looking project dir into ~/.ssh must also fail
	// via the realpath re-check.
	projParent := filepath.Join(home, "projects")
	if err := os.MkdirAll(projParent, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(projParent, "innocent")
	if err := os.Symlink(sshDir, link); err != nil {
		t.Fatal(err)
	}
	if err := validateLocalPath(link); err == nil {
		t.Error("validateLocalPath(symlink -> ~/.ssh) must fail")
	}

	// A normal project dir under home still passes.
	proj := filepath.Join(projParent, "app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateLocalPath(proj); err != nil {
		t.Errorf("legit project dir rejected: %v", err)
	}
}
