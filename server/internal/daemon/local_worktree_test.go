package daemon

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makeRepo initializes a git repo with one commit at dir.
func makeRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
}

func TestDetectLocalRepos_MultiRepoParent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	parent := t.TempDir()
	makeRepo(t, filepath.Join(parent, "backend"))
	makeRepo(t, filepath.Join(parent, "admin-dashboard"))
	makeRepo(t, filepath.Join(parent, "docs"))
	// A non-git subfolder and a dotfolder must be ignored.
	os.MkdirAll(filepath.Join(parent, "notarepo"), 0o755)
	os.MkdirAll(filepath.Join(parent, ".hidden"), 0o755)

	repos, err := detectLocalRepos(context.Background(), parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 3 {
		t.Fatalf("expected 3 repos, got %d: %+v", len(repos), repos)
	}
	// Sorted by name; each placed under its subfolder.
	if repos[0].Name != "admin-dashboard" || repos[0].RelPath != "admin-dashboard" {
		t.Errorf("repo[0] = %+v", repos[0])
	}
}

func TestDetectLocalRepos_SingleRepoParent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	parent := t.TempDir()
	makeRepo(t, parent) // the parent itself is the repo

	repos, err := detectLocalRepos(context.Background(), parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].RelPath != "." || repos[0].SrcPath != parent {
		t.Fatalf("single-repo parent should be one repo at '.', got %+v", repos)
	}
}

func TestProvisionLocalWorktrees_MultiRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	parent := t.TempDir()
	makeRepo(t, filepath.Join(parent, "backend"))
	makeRepo(t, filepath.Join(parent, "frontend"))
	ctx := context.Background()
	workDir := filepath.Join(t.TempDir(), "work")

	run, err := provisionLocalWorktrees(ctx, parent, "issue-abc123", workDir, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(run.worktrees) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(run.worktrees))
	}
	// Each worktree materialized under workDir/<repo> and is a real work tree.
	for _, name := range []string{"backend", "frontend"} {
		wt := filepath.Join(workDir, name)
		if !isGitWorkTree(ctx, wt) {
			t.Errorf("%s is not a git work tree", wt)
		}
		// On the expected agent branch.
		branch := gitAt(t, wt, "symbolic-ref", "--short", "HEAD")
		if branch != "agent/issue-ab/"+name && !strings.HasPrefix(branch, "agent/") {
			t.Errorf("%s branch = %q", name, branch)
		}
	}
	// The user's source repos are untouched (still on main).
	if b := gitAt(t, filepath.Join(parent, "backend"), "symbolic-ref", "--short", "HEAD"); b != "main" {
		t.Errorf("source repo moved off main: %q", b)
	}

	// Agent edits + commits in the backend worktree.
	wt := filepath.Join(workDir, "backend")
	os.WriteFile(filepath.Join(wt, "new.txt"), []byte("agent"), 0o644)
	gitAt(t, wt, "add", ".")
	gitAt(t, wt, "commit", "-q", "-m", "agent work")

	// Cleanup removes the worktrees but KEEPS the branch (holds the commit).
	cleanupWorktrees(ctx, run, slog.Default())
	if _, err := os.Stat(filepath.Join(workDir, "backend")); !os.IsNotExist(err) {
		t.Error("worktree dir should be removed after cleanup")
	}
	// The source repo has no dangling worktree metadata.
	if out := gitAt(t, filepath.Join(parent, "backend"), "worktree", "list"); strings.Count(out, "\n") > 0 {
		// only the main worktree should remain (one line, no newline trailing)
		if strings.Contains(out, "work/backend") {
			t.Errorf("dangling worktree after cleanup:\n%s", out)
		}
	}
	// The agent branch (with the commit) survives cleanup.
	branches := gitAt(t, filepath.Join(parent, "backend"), "branch", "--list", "agent/*")
	if !strings.Contains(branches, "agent/") {
		t.Errorf("agent branch should survive worktree cleanup, got: %q", branches)
	}
}

func TestProvisionLocalWorktrees_SingleRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	parent := t.TempDir()
	makeRepo(t, parent)
	ctx := context.Background()
	workDir := filepath.Join(t.TempDir(), "work") // must not pre-exist for single-repo

	run, err := provisionLocalWorktrees(ctx, parent, "issue-solo01", workDir, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(run.worktrees) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(run.worktrees))
	}
	// The worktree IS workDir itself (RelPath ".").
	if !isGitWorkTree(ctx, workDir) {
		t.Errorf("workDir should be the worktree for a single-repo parent")
	}
	cleanupWorktrees(ctx, run, slog.Default())
}

func TestProvisionLocalWorktrees_NoRepos(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	parent := t.TempDir() // empty, no git repos
	_, err := provisionLocalWorktrees(context.Background(), parent, "issue-x", filepath.Join(t.TempDir(), "w"), slog.Default())
	if err == nil {
		t.Fatal("expected an error when the parent has no git repositories")
	}
}

func TestProvisionLocalWorktreesAt_UsesPinnedBaseInsteadOfCurrentSourceHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	source := t.TempDir()
	makeRepo(t, source)
	pinned := gitAt(t, source, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(source, "later.txt"), []byte("later"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, source, "add", ".")
	gitAt(t, source, "commit", "-q", "-m", "later source change")
	current := gitAt(t, source, "rev-parse", "HEAD")
	if current == pinned {
		t.Fatal("fixture did not advance source HEAD")
	}

	workDir := filepath.Join(t.TempDir(), "worker")
	run, err := provisionLocalWorktreesAt(context.Background(), source, "step-worker", workDir, []OrchestrationGitHead{{
		Repo: filepath.Base(source), HeadSHA: pinned,
	}}, false, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupWorktrees(context.Background(), run, slog.Default())
	if got := gitAt(t, workDir, "rev-parse", "HEAD"); got != pinned {
		t.Fatalf("worker HEAD = %s, want pinned run base %s", got, pinned)
	}
	if run.worktrees[0].BaseSHA != pinned {
		t.Fatalf("reported base = %s, want %s", run.worktrees[0].BaseSHA, pinned)
	}
}

func TestProvisionLocalWorktreesAt_ReadOnlyUsesDetachedIntegrationHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	source := t.TempDir()
	makeRepo(t, source)
	gitAt(t, source, "checkout", "-q", "-b", "integration")
	if err := os.WriteFile(filepath.Join(source, "integrated.txt"), []byte("merged"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, source, "add", ".")
	gitAt(t, source, "commit", "-q", "-m", "integrated result")
	integrationHead := gitAt(t, source, "rev-parse", "HEAD")
	gitAt(t, source, "checkout", "-q", "main")

	workDir := filepath.Join(t.TempDir(), "verify")
	run, err := provisionLocalWorktreesAt(context.Background(), source, "qa-step", workDir, []OrchestrationGitHead{{
		Repo: filepath.Base(source), HeadSHA: integrationHead,
	}}, true, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupWorktrees(context.Background(), run, slog.Default())
	if got := gitAt(t, workDir, "rev-parse", "HEAD"); got != integrationHead {
		t.Fatalf("verification HEAD = %s, want integration HEAD %s", got, integrationHead)
	}
	cmd := exec.Command("git", "-C", workDir, "symbolic-ref", "--short", "HEAD")
	if err := cmd.Run(); err == nil {
		t.Fatal("read-only verification worktree must be detached")
	}
	if run.worktrees[0].Branch != "" {
		t.Fatalf("read-only verification unexpectedly created branch %q", run.worktrees[0].Branch)
	}
}

// This is the local orchestration contract exercised end to end against a real
// Git repository: two workers branch from one pinned commit, integration must
// contain both exact worker heads, and QA/review receive parallel detached
// worktrees at the exact integrated result.
func TestLocalOrchestrationSmoke_ParallelBranchesIntegrateAndVerifyExactHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	source := t.TempDir()
	makeRepo(t, source)
	repoName := filepath.Base(source)
	base := gitAt(t, source, "rev-parse", "HEAD")
	baseRefs := []OrchestrationGitHead{{Repo: repoName, HeadSHA: base}}

	workerRoot := t.TempDir()
	backendDir := filepath.Join(workerRoot, "backend")
	frontendDir := filepath.Join(workerRoot, "frontend")
	backendRun, err := provisionLocalWorktreesAt(ctx, source, "backend-step", backendDir, baseRefs, false, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupWorktrees(ctx, backendRun, slog.Default())
	frontendRun, err := provisionLocalWorktreesAt(ctx, source, "frontend-step", frontendDir, baseRefs, false, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupWorktrees(ctx, frontendRun, slog.Default())

	if err := os.WriteFile(filepath.Join(backendDir, "api.txt"), []byte("backend contract\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, backendDir, "add", "api.txt")
	gitAt(t, backendDir, "commit", "-q", "-m", "backend worker")
	backendHead := gitAt(t, backendDir, "rev-parse", "HEAD")

	if err := os.WriteFile(filepath.Join(frontendDir, "web.txt"), []byte("frontend contract\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, frontendDir, "add", "web.txt")
	gitAt(t, frontendDir, "commit", "-q", "-m", "frontend worker")
	frontendHead := gitAt(t, frontendDir, "rev-parse", "HEAD")
	if backendHead == frontendHead || gitAt(t, backendDir, "rev-parse", "HEAD^") != base || gitAt(t, frontendDir, "rev-parse", "HEAD^") != base {
		t.Fatal("workers did not produce independent branches from the pinned base")
	}

	integrationDir := filepath.Join(t.TempDir(), "integration")
	integrationRun, err := provisionLocalWorktreesAt(ctx, source, "integration-step", integrationDir, baseRefs, false, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupWorktrees(ctx, integrationRun, slog.Default())
	gitAt(t, integrationDir, "merge", "--no-ff", "-q", "-m", "merge backend", backendHead)
	gitAt(t, integrationDir, "merge", "--no-ff", "-q", "-m", "merge frontend", frontendHead)
	integrationHead := gitAt(t, integrationDir, "rev-parse", "HEAD")
	state := verifyIntegratedHeads(ctx, []string{integrationDir}, []OrchestrationGitDependency{
		{Key: "backend", HeadSHA: backendHead},
		{Key: "frontend", HeadSHA: frontendHead},
	}, worktreeMergeState{HeadSHA: integrationHead, Status: "clean"})
	if state.IntegrationStatus != "complete" || len(state.IntegratedHeadSHAs) != 2 || len(state.MissingHeadSHAs) != 0 {
		t.Fatalf("integration did not prove both worker heads: %+v", state)
	}

	verificationRefs := []OrchestrationGitHead{{Repo: repoName, HeadSHA: integrationHead}}
	qaDir := filepath.Join(t.TempDir(), "qa")
	qaRun, err := provisionLocalWorktreesAt(ctx, source, "qa-step", qaDir, verificationRefs, true, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupWorktrees(ctx, qaRun, slog.Default())
	reviewDir := filepath.Join(t.TempDir(), "review")
	reviewRun, err := provisionLocalWorktreesAt(ctx, source, "review-step", reviewDir, verificationRefs, true, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupWorktrees(ctx, reviewRun, slog.Default())

	for _, dir := range []string{qaDir, reviewDir} {
		if got := gitAt(t, dir, "rev-parse", "HEAD"); got != integrationHead {
			t.Fatalf("verification HEAD = %s, want %s", got, integrationHead)
		}
		if gitAt(t, dir, "status", "--porcelain") != "" {
			t.Fatalf("verification worktree %q is dirty", dir)
		}
		if _, err := os.Stat(filepath.Join(dir, "api.txt")); err != nil {
			t.Fatalf("verification worktree missing backend handoff: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "web.txt")); err != nil {
			t.Fatalf("verification worktree missing frontend handoff: %v", err)
		}
		if err := exec.Command("git", "-C", dir, "symbolic-ref", "--short", "HEAD").Run(); err == nil {
			t.Fatalf("verification worktree %q is not detached", dir)
		}
	}
	if got := gitAt(t, source, "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Fatalf("source checkout moved to %q", got)
	}
}

func TestSanitizeResourcesForWorktree(t *testing.T) {
	refs := []ProjectResourceData{
		{ResourceType: "local_directory", ResourceRef: []byte(`{"local_path":"/Users/me/src","daemon_id":"d1","label":"mine"}`)},
		{ResourceType: "github_repo", ResourceRef: []byte(`{"url":"ssh://x/y.git"}`)},
	}
	// in_place: unchanged.
	if out := sanitizeResourcesForWorktree(refs, false); string(out[0].ResourceRef) != `{"local_path":"/Users/me/src","daemon_id":"d1","label":"mine"}` {
		t.Error("in_place must not strip")
	}
	// worktree: local_path + daemon_id stripped, github_repo untouched.
	out := sanitizeResourcesForWorktree(refs, true)
	if strings.Contains(string(out[0].ResourceRef), "/Users/me/src") || strings.Contains(string(out[0].ResourceRef), "daemon_id") {
		t.Errorf("worktree must strip source path/daemon: %s", out[0].ResourceRef)
	}
	if !strings.Contains(string(out[0].ResourceRef), "mine") {
		t.Errorf("label should be kept: %s", out[0].ResourceRef)
	}
	if string(out[1].ResourceRef) != `{"url":"ssh://x/y.git"}` {
		t.Errorf("github_repo must be untouched: %s", out[1].ResourceRef)
	}
	// The input slice must not be mutated.
	if strings.Contains(string(refs[0].ResourceRef), "isolation") {
		t.Error("original input mutated")
	}
}

func TestFinalizeWorktrees_CommitsAndCleans(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	parent := t.TempDir()
	makeRepo(t, filepath.Join(parent, "svc"))
	ctx := context.Background()
	workDir := filepath.Join(t.TempDir(), "work")
	run, err := provisionLocalWorktrees(ctx, parent, "issue-fin01", workDir, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(workDir, "svc")
	// Agent edits WITHOUT committing, and a sidecar is written.
	os.WriteFile(filepath.Join(wt, "index.js"), []byte("v2"), 0o644)
	os.WriteFile(filepath.Join(wt, "CLAUDE.md"), []byte("brief"), 0o644)

	summary := finalizeWorktrees(ctx, run, "dev-agent", slog.Default())
	if summary == "" {
		t.Fatal("expected a summary when the agent changed files")
	}
	branch := run.worktrees[0].Branch
	// The real edit is committed on the branch; the sidecar is NOT.
	got := gitAt(t, filepath.Join(parent, "svc"), "show", branch+":index.js")
	if got != "v2" {
		t.Errorf("agent edit not committed to branch: %q", got)
	}
	if err := exec.Command("git", "-C", filepath.Join(parent, "svc"), "cat-file", "-e", branch+":CLAUDE.md").Run(); err == nil {
		t.Error("sidecar CLAUDE.md must NOT be committed")
	}
	// Worktree removed, source clean on main.
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Error("worktree should be removed")
	}
	if b := gitAt(t, filepath.Join(parent, "svc"), "symbolic-ref", "--short", "HEAD"); b != "main" {
		t.Errorf("source moved off main: %q", b)
	}
	if s := gitAt(t, filepath.Join(parent, "svc"), "status", "--porcelain"); s != "" {
		t.Errorf("source dirty after finalize: %q", s)
	}
}

func TestInspectWorktreeMergeState_DetectsConflictWithoutMutatingSource(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	source := t.TempDir()
	makeRepo(t, source)
	ctx := context.Background()
	workDir := filepath.Join(t.TempDir(), "attempt")
	run, err := provisionLocalWorktrees(ctx, source, "step01-attempt01", workDir, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	commit := func(dir, content, message string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("git", "-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "add", "f.txt")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("add: %v %s", err, out)
		}
		cmd = exec.Command("git", "-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", message)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("commit: %v %s", err, out)
		}
	}
	commit(workDir, "agent change", "agent")
	commit(source, "main change", "main")
	state := inspectWorktreeMergeState(ctx, run)
	if state.Status != "conflicts" {
		t.Fatalf("expected conflicts, got %+v", state)
	}
	if len(state.ConflictFiles) != 1 || state.ConflictFiles[0] != "f.txt" {
		t.Fatalf("unexpected files: %+v", state.ConflictFiles)
	}
	if got := gitAt(t, source, "show", "HEAD:f.txt"); got != "main change" {
		t.Fatalf("source mutated: %q", got)
	}
}

func TestInspectManagedWorktreeMergeState_UsesRemoteDefaultBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	source := t.TempDir()
	makeRepo(t, source)
	ctx := context.Background()
	workRoot := t.TempDir()
	workDir := filepath.Join(workRoot, "managed-repo")
	run, err := provisionLocalWorktrees(ctx, source, "managed-attempt", workDir, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	commit := func(dir, content, message string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("git", "-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "add", "f.txt")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("add: %v %s", err, out)
		}
		cmd = exec.Command("git", "-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", message)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("commit: %v %s", err, out)
		}
	}
	commit(workDir, "agent change", "agent")
	commit(source, "remote change", "remote")
	remoteHead := gitAt(t, source, "rev-parse", "HEAD")
	if out, err := exec.Command("git", "-C", source, "update-ref", "refs/remotes/origin/main", remoteHead).CombinedOutput(); err != nil {
		t.Fatalf("update-ref: %v %s", err, out)
	}
	if out, err := exec.Command("git", "-C", source, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main").CombinedOutput(); err != nil {
		t.Fatalf("symbolic-ref: %v %s", err, out)
	}
	state := inspectManagedWorktreeMergeState(ctx, workRoot)
	if state.Status != "conflicts" {
		t.Fatalf("expected managed conflict, got %+v", state)
	}
	if state.Branch != run.worktrees[0].Branch {
		t.Fatalf("branch = %q, want %q", state.Branch, run.worktrees[0].Branch)
	}
	if len(state.ConflictFiles) != 1 || state.ConflictFiles[0] != "f.txt" {
		t.Fatalf("unexpected files: %+v", state.ConflictFiles)
	}
}

func TestVerifyIntegratedHeadsRequiresEveryDependency(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	makeRepo(t, repo)
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commitFile := func(branch, name string) string {
		t.Helper()
		runGit("checkout", "-q", "main")
		runGit("checkout", "-q", "-b", branch)
		if err := os.WriteFile(filepath.Join(repo, name), []byte(branch), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit("add", name)
		runGit("commit", "-q", "-m", branch)
		return gitAt(t, repo, "rev-parse", "HEAD")
	}
	firstHead := commitFile("dependency-one", "one.txt")
	secondHead := commitFile("dependency-two", "two.txt")
	runGit("checkout", "-q", "main")
	runGit("checkout", "-q", "-b", "integration")
	runGit("merge", "--no-ff", "-q", "-m", "merge first", firstHead)

	dependencies := []OrchestrationGitDependency{
		{Key: "one", HeadSHA: firstHead},
		{Key: "two", HeadSHA: secondHead},
	}
	state := verifyIntegratedHeads(context.Background(), []string{repo}, dependencies, worktreeMergeState{
		HeadSHA: gitAt(t, repo, "rev-parse", "HEAD"), Status: "clean",
	})
	if state.IntegrationStatus != "missing_heads" || len(state.IntegratedHeadSHAs) != 1 || len(state.MissingHeadSHAs) != 1 || state.MissingHeadSHAs[0] != secondHead {
		t.Fatalf("expected one missing dependency, got %+v", state)
	}

	runGit("merge", "--no-ff", "-q", "-m", "merge second", secondHead)
	state = verifyIntegratedHeads(context.Background(), []string{repo}, dependencies, worktreeMergeState{
		HeadSHA: gitAt(t, repo, "rev-parse", "HEAD"), Status: "clean",
	})
	if state.IntegrationStatus != "complete" || len(state.IntegratedHeadSHAs) != 2 || len(state.MissingHeadSHAs) != 0 {
		t.Fatalf("expected complete integration, got %+v", state)
	}
}

func TestProvisionOrReuse_ReusesDevWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	parent := t.TempDir()
	makeRepo(t, filepath.Join(parent, "svc"))
	ctx := context.Background()
	env := filepath.Join(t.TempDir(), "wt", "issue-x")

	// First task (dev): provision.
	run1, reused1, err := provisionOrReuseWorktrees(ctx, parent, "issue-xyz", env, slog.Default())
	if err != nil || reused1 {
		t.Fatalf("first call should provision (reused=%v err=%v)", reused1, err)
	}
	wt := run1.worktrees[0].Path
	// Dev makes an uncommitted change in its worktree.
	os.WriteFile(filepath.Join(wt, "index.js"), []byte("dev change"), 0o644)

	// Second task (QA, same issue): must REUSE the same worktree and see the
	// dev's change.
	run2, reused2, err := provisionOrReuseWorktrees(ctx, parent, "issue-xyz", env, slog.Default())
	if err != nil || !reused2 {
		t.Fatalf("second call should reuse (reused=%v err=%v)", reused2, err)
	}
	if run2.worktrees[0].Path != wt {
		t.Errorf("reuse gave a different worktree path: %q vs %q", run2.worktrees[0].Path, wt)
	}
	got, _ := os.ReadFile(filepath.Join(wt, "index.js"))
	if string(got) != "dev change" {
		t.Errorf("QA must see the dev's change, got %q", got)
	}
}

func TestSweepWorktreeEnvs_RemovesStale(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	ws := "ws1"
	parent := t.TempDir()
	makeRepo(t, filepath.Join(parent, "svc"))
	ctx := context.Background()
	env := filepath.Join(root, ws, ".worktrees", "issue-old")
	if _, err := provisionLocalWorktrees(ctx, parent, "issue-old", env, slog.Default()); err != nil {
		t.Fatal(err)
	}
	// Backdate the env dir so it looks idle past the TTL.
	old := time.Now().Add(-72 * time.Hour)
	os.Chtimes(env, old, old)

	d := &Daemon{cfg: Config{WorkspacesRoot: root}}
	d.sweepWorktreeEnvs(ctx, nil, slog.Default())

	if _, err := os.Stat(env); !os.IsNotExist(err) {
		t.Error("stale worktree env should be swept")
	}
	// The source repo has no dangling worktree metadata.
	if out := gitAt(t, filepath.Join(parent, "svc"), "worktree", "list"); strings.Contains(out, ".worktrees") {
		t.Errorf("dangling worktree after sweep:\n%s", out)
	}
}
