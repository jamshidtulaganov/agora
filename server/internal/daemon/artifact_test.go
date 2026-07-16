package daemon

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func artifactGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	// CI runners have no global git identity; commits fail without one.
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func artifactRepoFixture(t *testing.T) (string, ArtifactCapabilityGrant) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	makeRepo(t, repo)
	base := artifactGit(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("exact artifact content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifactGit(t, repo, "add", "feature.txt")
	artifactGit(t, repo, "commit", "-q", "-m", "artifact head")
	head := artifactGit(t, repo, "rev-parse", "HEAD")
	return repo, ArtifactCapabilityGrant{
		ArtifactID: "exact-artifact", SourceRoot: repo,
		Repos: []ArtifactRepoRef{{Repo: filepath.Base(repo), BaseSHA: base, HeadSHA: head, MergeStatus: "clean"}},
	}
}

func TestArtifactChangesAndFileReadExactGitObjects(t *testing.T) {
	repo, grant := artifactRepoFixture(t)
	changes, err := artifactChanges(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || len(changes[0].Files) != 1 || changes[0].Files[0].Path != "feature.txt" || !strings.Contains(changes[0].Diff, "+exact artifact content") {
		t.Fatalf("unexpected artifact changes: %+v", changes)
	}
	foundTreeFile := false
	for _, path := range changes[0].Tree {
		foundTreeFile = foundTreeFile || path == "feature.txt"
	}
	if !foundTreeFile {
		t.Fatalf("artifact tree omitted feature.txt: %v", changes[0].Tree)
	}
	file, err := artifactFile(context.Background(), grant, "", "feature.txt")
	if err != nil || file.Content != "exact artifact content\n" || file.HeadSHA != grant.Repos[0].HeadSHA {
		t.Fatalf("exact file read failed: file=%+v err=%v", file, err)
	}
	if _, err := artifactFile(context.Background(), grant, "", "../outside"); err == nil {
		t.Fatal("path traversal was accepted")
	}
	if got := artifactGit(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("read-only artifact operations dirtied source: %q", got)
	}
}

func TestArtifactRuntimeIsDetachedDisposableAndLeavesSourceUntouched(t *testing.T) {
	repo, grant := artifactRepoFixture(t)
	d := &Daemon{logger: slog.Default()}
	runtime, err := d.provisionArtifactRuntime(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	runtimeRoot := runtime.root
	worktree := runtime.run.worktrees[0].Path
	if got := artifactGit(t, worktree, "rev-parse", "HEAD"); got != grant.Repos[0].HeadSHA {
		t.Fatalf("runtime HEAD=%s want=%s", got, grant.Repos[0].HeadSHA)
	}
	if err := exec.Command("git", "-C", worktree, "symbolic-ref", "--short", "HEAD").Run(); err == nil {
		t.Fatal("artifact runtime is attached to a branch")
	}
	if err := os.WriteFile(filepath.Join(worktree, "package-lock.json"), []byte("generated only in disposable runtime\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := artifactGit(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("disposable runtime mutation reached source: %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "package-lock.json")); !os.IsNotExist(err) {
		t.Fatalf("source gained generated package-lock: %v", err)
	}
	runtime.cleanup(context.Background(), d.logger)
	if _, err := os.Stat(runtimeRoot); !os.IsNotExist(err) {
		t.Fatalf("disposable runtime was not removed: %v", err)
	}
	if got := artifactGit(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("cleanup mutated source: %q", got)
	}
}

func TestArtifactLiveChangesAndFileReadWorkingTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	makeRepo(t, repo) // commits an initial file
	// A tracked edit + a brand-new untracked file: both must appear live.
	if err := os.WriteFile(filepath.Join(repo, "app.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifactGit(t, repo, "add", "app.txt")
	artifactGit(t, repo, "commit", "-q", "-m", "app v1")
	if err := os.WriteFile(filepath.Join(repo, "app.txt"), []byte("v1\nedited uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "brand_new.txt"), []byte("fresh untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	grant := ArtifactCapabilityGrant{
		ArtifactID: "live", Purpose: "changes", SourceRoot: repo, Live: true,
	}
	changes, err := artifactLiveChanges(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("want 1 repo, got %d", len(changes))
	}
	c := changes[0]
	if c.HeadSHA != "working" {
		t.Errorf("live head should be \"working\", got %q", c.HeadSHA)
	}
	if !strings.Contains(c.Diff, "edited uncommitted") {
		t.Errorf("live diff missing the uncommitted edit: %s", c.Diff)
	}
	if !strings.Contains(c.Diff, "fresh untracked") {
		t.Errorf("live diff missing untracked content: %s", c.Diff)
	}
	paths := map[string]bool{}
	for _, f := range c.Files {
		paths[f.Path] = true
	}
	if !paths["app.txt"] || !paths["brand_new.txt"] {
		t.Errorf("changed files missing tracked/untracked entries: %+v", c.Files)
	}

	// Live file read returns current on-disk bytes (incl. uncommitted edit).
	file, err := artifactLiveFile(context.Background(), ArtifactCapabilityGrant{SourceRoot: repo, Live: true}, "", "app.txt")
	if err != nil {
		t.Fatal(err)
	}
	if file.Content != "v1\nedited uncommitted\n" || file.HeadSHA != "working" {
		t.Fatalf("live file read wrong: %+v", file)
	}

	// Path traversal stays blocked.
	if _, err := artifactLiveFile(context.Background(), ArtifactCapabilityGrant{SourceRoot: repo, Live: true}, "", "../escape"); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
}

func TestLiveArtifactPreviewJSON(t *testing.T) {
	withURL := liveArtifactPreviewJSON(ArtifactCapabilityGrant{ArtifactID: "a", Live: true, PreviewURL: "http://localhost:3000"})
	if withURL["running"] != true || withURL["url"] != "http://localhost:3000" {
		t.Fatalf("configured preview should be running with the url: %+v", withURL)
	}
	if _, ok := withURL["needs_command"]; ok {
		t.Error("configured preview must not signal needs_command")
	}
	noURL := liveArtifactPreviewJSON(ArtifactCapabilityGrant{ArtifactID: "a", Live: true})
	if noURL["running"] != false || noURL["needs_command"] != true {
		t.Fatalf("unset preview should ask for a dev server: %+v", noURL)
	}
}
