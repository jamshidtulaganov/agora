package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Worktree isolation for local_directory (isolation:"worktree"). Instead of
// running the agent directly in the user's folder, the daemon provisions an
// isolated git worktree per repo — cut from the developer's OWN local checkout
// — inside the managed env. Tasks on different issues get different worktrees
// and run in parallel; the user's checkout is never the agent's working tree.
//
// Multi-repo layout: the local_directory local_path is a PARENT folder that
// may contain several repo checkouts as subfolders (backend/, frontend/,
// docs/). A parent that is itself a single git repo (no repo subfolders) is
// the single-repo case. Which repos a task actually edits is up to the agent
// (task-description-driven) — the daemon just makes all of them available as
// worktrees.

// localRepo is one repo the daemon will provision a worktree for.
type localRepo struct {
	Name    string // subfolder name, or the repo basename for a single-repo parent
	SrcPath string // the developer's local checkout to cut the worktree from
	RelPath string // placement under the env workdir: "." for single-repo, else Name
}

// detectLocalRepos enumerates the repos under a local_directory parent. If the
// parent itself is a git work tree it is the single repo (RelPath "."). Else,
// immediate subdirectories that are git work trees are the repos. Returns an
// error only when the parent is unreadable; an empty result means "no git repo
// here" (caller should fail the task with a clear message).
func detectLocalRepos(ctx context.Context, parent string) ([]localRepo, error) {
	if isGitWorkTree(ctx, parent) {
		return []localRepo{{Name: filepath.Base(parent), SrcPath: parent, RelPath: "."}}, nil
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil, fmt.Errorf("read local_directory parent %q: %w", parent, err)
	}
	var repos []localRepo
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		sub := filepath.Join(parent, e.Name())
		if isGitWorkTree(ctx, sub) {
			repos = append(repos, localRepo{Name: e.Name(), SrcPath: sub, RelPath: e.Name()})
		}
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	return repos, nil
}

// detectLocalReposForRoots expands one primary plus any additional local
// folders into a single stable repository layout. The one-root case preserves
// the historic "." placement. With several roots every repository gets its
// basename as a sibling under the managed workdir, so the primary remains the
// first folder while all attached repos are writable and independently
// represented in orchestration git evidence.
func detectLocalReposForRoots(ctx context.Context, roots []string) ([]localRepo, error) {
	if len(roots) == 0 {
		return nil, nil
	}
	if len(roots) == 1 {
		return detectLocalRepos(ctx, roots[0])
	}
	var result []localRepo
	seenNames := map[string]string{}
	seenSources := map[string]bool{}
	for _, root := range roots {
		repos, err := detectLocalRepos(ctx, root)
		if err != nil {
			return nil, err
		}
		if len(repos) == 0 {
			return nil, fmt.Errorf("local_directory %q contains no git repositories", root)
		}
		for _, repo := range repos {
			source := filepath.Clean(repo.SrcPath)
			if seenSources[source] {
				continue
			}
			name := filepath.Base(source)
			if prior, exists := seenNames[name]; exists && prior != source {
				return nil, fmt.Errorf("local_directory repositories %q and %q share folder name %q; rename one folder before attaching both", prior, source, name)
			}
			seenNames[name] = source
			seenSources[source] = true
			result = append(result, localRepo{Name: name, SrcPath: source, RelPath: name})
		}
	}
	return result, nil
}

// provisionedWorktree records a created worktree so it can be cleaned up.
type provisionedWorktree struct {
	SrcRepo string // the developer's checkout the worktree hangs off
	Path    string // the worktree directory inside the env
	Branch  string // feature/<issue>, fix/<issue>, or a unique orchestration branch
	BaseSHA string // source HEAD when this isolated attempt was created
}

// worktreeRun is the result of provisioning: the created worktrees + the
// per-repo branch names, for cleanup and summary.
type worktreeRun struct {
	parent    string
	worktrees []provisionedWorktree
}

// provisionLocalWorktrees is the execenv ProvisionWorkDir hook body: for each
// repo under the parent, `git worktree add <target> -b agent/<issue>/<repo>
// HEAD`, mirroring the parent's subfolder layout inside workDir. issueKey is a
// short, filesystem-safe issue identifier used in the branch name so a repo's
// worktrees are reused per issue (dev → QA → fix).
func provisionLocalWorktrees(ctx context.Context, parent, issueKey, workDir string, log *slog.Logger) (*worktreeRun, error) {
	return provisionLocalWorktreesAt(ctx, parent, issueKey, workDir, nil, false, log)
}

// provisionLocalWorktreesAt creates each worktree from an explicit immutable
// per-repository commit when baseRefs is non-empty. Read-only verification
// worktrees are detached at that commit and never receive an agent branch.
func provisionLocalWorktreesAt(ctx context.Context, parent, issueKey, workDir string, baseRefs []OrchestrationGitHead, readOnly bool, log *slog.Logger) (*worktreeRun, error) {
	repos, err := detectLocalRepos(ctx, parent)
	if err != nil {
		return nil, err
	}
	return provisionLocalWorktreesFromReposAt(ctx, repos, parent, issueKey, workDir, baseRefs, readOnly, log)
}

func provisionLocalWorktreesForRootsAt(ctx context.Context, roots []string, issueKey, workDir string, baseRefs []OrchestrationGitHead, readOnly bool, log *slog.Logger) (*worktreeRun, error) {
	repos, err := detectLocalReposForRoots(ctx, roots)
	if err != nil {
		return nil, err
	}
	return provisionLocalWorktreesFromReposAt(ctx, repos, strings.Join(roots, string(os.PathListSeparator)), issueKey, workDir, baseRefs, readOnly, log)
}

func provisionLocalWorktreesFromReposAt(ctx context.Context, repos []localRepo, parent, issueKey, workDir string, baseRefs []OrchestrationGitHead, readOnly bool, log *slog.Logger, preferredBranch ...string) (*worktreeRun, error) {
	if len(repos) == 0 {
		return nil, fmt.Errorf("local_directory %q contains no git repositories (worktree isolation needs at least one)", parent)
	}
	run := &worktreeRun{parent: parent}

	// For a single repo placed at "." the worktree IS workDir, which must not
	// pre-exist (the ProvisionWorkDir hook runs in lieu of MkdirAll). For
	// multiple repos, create workDir first and place each under it.
	single := len(repos) == 1 && repos[0].RelPath == "."
	if !single {
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			return nil, fmt.Errorf("create worktree parent %q: %w", workDir, err)
		}
	}

	for _, repo := range repos {
		target := workDir
		if !single {
			target = filepath.Join(workDir, repo.Name)
		}
		baseRef := "HEAD"
		if len(baseRefs) > 0 {
			baseRef = orchestrationBaseRefForRepo(baseRefs, repo.Name)
			if baseRef == "" {
				cleanupWorktrees(ctx, run, log)
				return nil, fmt.Errorf("orchestration base snapshot has no commit for repository %q", repo.Name)
			}
		}
		branch := localAgentBranchName(issueKey, repo.Name)
		if len(preferredBranch) > 0 && strings.TrimSpace(preferredBranch[0]) != "" {
			branch = strings.TrimSpace(preferredBranch[0])
		}
		if readOnly {
			branch = ""
		}
		actualBranch, err := addWorktreeAt(ctx, repo.SrcPath, target, branch, baseRef, readOnly)
		if err != nil {
			// Roll back anything already created so a partial failure doesn't
			// leak worktrees in the user's repos.
			cleanupWorktrees(ctx, run, log)
			return nil, err
		}
		branch = actualBranch
		baseSHA, _ := gitOutput(ctx, target, "rev-parse", "HEAD")
		run.worktrees = append(run.worktrees, provisionedWorktree{SrcRepo: repo.SrcPath, Path: target, Branch: branch, BaseSHA: baseSHA})
	}
	return run, nil
}

func orchestrationBaseRefForRepo(baseRefs []OrchestrationGitHead, repo string) string {
	// A single repository's name is derived from the source checkout basename.
	// When the source is itself a linked integration worktree that basename is
	// the task directory, not the original repository name recorded in Git
	// evidence. There is only one possible mapping, so use it directly.
	if len(baseRefs) == 1 {
		return strings.TrimSpace(baseRefs[0].HeadSHA)
	}
	for _, base := range baseRefs {
		if base.Repo == repo {
			return strings.TrimSpace(base.HeadSHA)
		}
	}
	return ""
}

// snapshotLocalRepoHeads returns the full source HEAD for every repository in
// a local_directory. The server compare-and-set endpoint turns this proposal
// into the immutable run base used by all parallel workers.
func snapshotLocalRepoHeads(ctx context.Context, parent string) ([]OrchestrationGitHead, error) {
	repos, err := detectLocalRepos(ctx, parent)
	if err != nil {
		return nil, err
	}
	if len(repos) == 0 {
		return nil, fmt.Errorf("local_directory %q contains no git repositories", parent)
	}
	refs := make([]OrchestrationGitHead, 0, len(repos))
	for _, repo := range repos {
		head, headErr := gitOutput(ctx, repo.SrcPath, "rev-parse", "HEAD")
		if headErr != nil {
			return nil, fmt.Errorf("resolve HEAD for repository %q: %w", repo.Name, headErr)
		}
		if head == "" {
			return nil, fmt.Errorf("resolve HEAD for repository %q: empty commit", repo.Name)
		}
		refs = append(refs, OrchestrationGitHead{Repo: repo.Name, HeadSHA: head})
	}
	return refs, nil
}

func snapshotLocalRepoHeadsForRoots(ctx context.Context, roots []string, preferredBranch string) ([]OrchestrationGitHead, error) {
	repos, err := detectLocalReposForRoots(ctx, roots)
	if err != nil {
		return nil, err
	}
	if len(repos) == 0 {
		return nil, errors.New("local_directory contains no git repositories")
	}
	refs := make([]OrchestrationGitHead, 0, len(repos))
	for _, repo := range repos {
		ref := "HEAD"
		branch := ""
		if candidate := strings.TrimSpace(preferredBranch); candidate != "" {
			// A project can attach several repositories and the issue branch may
			// exist in only some of them. Prefer it per repository, falling back
			// to that source checkout's HEAD for backwards compatibility.
			if _, verifyErr := gitOutput(ctx, repo.SrcPath, "rev-parse", "--verify", candidate+"^{commit}"); verifyErr == nil {
				ref = candidate
				branch = candidate
			}
		}
		head, headErr := gitOutput(ctx, repo.SrcPath, "rev-parse", ref)
		if headErr != nil {
			return nil, fmt.Errorf("resolve %s for repository %q: %w", ref, repo.Name, headErr)
		}
		if head == "" {
			return nil, fmt.Errorf("resolve %s for repository %q: empty commit", ref, repo.Name)
		}
		refs = append(refs, OrchestrationGitHead{Repo: repo.Name, Branch: branch, HeadSHA: head})
	}
	return refs, nil
}

type worktreeMergeState struct {
	Branch             string
	BaseSHA            string
	HeadSHA            string
	Status             string
	ConflictFiles      []string
	IntegrationStatus  string
	IntegratedHeadSHAs []string
	IntegratedHeads    []OrchestrationGitHead
	MissingHeadSHAs    []string
	// RepoStates holds one entry per provisioned repo. The single-value fields
	// above mirror the primary (first) repo plus the aggregated worst status,
	// for older servers and existing readers.
	RepoStates []RepoGitState
}

// mergeStatusSeverity orders per-repo statuses so a multi-repo aggregate
// surfaces the worst one: a conflict anywhere beats uncommitted anywhere
// beats an unreadable repo beats clean.
var mergeStatusSeverity = map[string]int{"clean": 0, "not_checked": 1, "unavailable": 2, "uncommitted": 3, "conflicts": 4}

func worseMergeStatus(a, b string) string {
	if mergeStatusSeverity[b] > mergeStatusSeverity[a] {
		return b
	}
	return a
}

// applyRepoStates mirrors the primary (first) repo into the legacy
// single-value fields and folds every repo's status into the aggregate
// Status/ConflictFiles. Multi-repo conflict paths get a "repo/" prefix so the
// aggregate list stays unambiguous.
func applyRepoStates(state *worktreeMergeState) {
	if len(state.RepoStates) == 0 {
		return
	}
	primary := state.RepoStates[0]
	state.Branch, state.BaseSHA, state.HeadSHA = primary.Branch, primary.BaseSHA, primary.HeadSHA
	state.Status = primary.MergeStatus
	state.ConflictFiles = nil
	multi := len(state.RepoStates) > 1
	for _, repoState := range state.RepoStates {
		state.Status = worseMergeStatus(state.Status, repoState.MergeStatus)
		for _, file := range repoState.ConflictFiles {
			if multi {
				state.ConflictFiles = append(state.ConflictFiles, repoState.Repo+"/"+file)
			} else {
				state.ConflictFiles = append(state.ConflictFiles, file)
			}
		}
	}
}

// dependencyHead is one commit the integration HEAD must contain, flattened
// from a dependency's per-repo heads (or its legacy single head).
type dependencyHead struct {
	key  string
	repo string
	sha  string
}

func requiredDependencyHeads(dependencies []OrchestrationGitDependency) []dependencyHead {
	var heads []dependencyHead
	seen := map[string]bool{}
	for _, dependency := range dependencies {
		expanded := dependency.Heads
		if len(expanded) == 0 {
			expanded = []OrchestrationGitHead{{Branch: dependency.Branch, HeadSHA: dependency.HeadSHA}}
		}
		for _, head := range expanded {
			sha := strings.TrimSpace(head.HeadSHA)
			if sha == "" {
				heads = append(heads, dependencyHead{key: dependency.Key, repo: strings.TrimSpace(head.Repo)})
				continue
			}
			repo := strings.TrimSpace(head.Repo)
			identity := repo + "\x00" + sha
			if seen[identity] {
				continue
			}
			seen[identity] = true
			heads = append(heads, dependencyHead{key: dependency.Key, repo: repo, sha: sha})
		}
	}
	return heads
}

// integratedInRepos reports whether sha is an ancestor of HEAD in the repo
// that actually contains it. A commit that exists in some repo but is not an
// ancestor of that repo's HEAD is NOT integrated — another repo must not mask
// that.
func integratedInRepos(ctx context.Context, repoDirs []string, repo, sha string) bool {
	for _, dir := range repoDirs {
		if repo != "" && !repoDirMatchesOrchestrationRef(ctx, dir, repo) {
			continue
		}
		if exec.CommandContext(ctx, "git", "-C", dir, "cat-file", "-e", sha+"^{commit}").Run() != nil {
			continue
		}
		if exec.CommandContext(ctx, "git", "-C", dir, "merge-base", "--is-ancestor", sha, "HEAD").Run() == nil {
			return true
		}
	}
	return false
}

func repoDirMatchesOrchestrationRef(ctx context.Context, dir, repo string) bool {
	want := strings.TrimSuffix(strings.TrimSpace(repo), ".git")
	if strings.EqualFold(strings.TrimSuffix(filepath.Base(filepath.Clean(dir)), ".git"), want) {
		return true
	}
	if remote, err := gitOutput(ctx, dir, "config", "--get", "remote.origin.url"); err == nil {
		trimmed := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(remote), "/"), ".git")
		if separator := strings.LastIndexAny(trimmed, "/:"); separator >= 0 {
			trimmed = trimmed[separator+1:]
		}
		if strings.EqualFold(trimmed, want) {
			return true
		}
	}
	if commonDir, err := gitOutput(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		commonDir = filepath.Clean(commonDir)
		if marker := strings.Index(commonDir, string(filepath.Separator)+".git"+string(filepath.Separator)+"worktrees"+string(filepath.Separator)); marker >= 0 {
			commonDir = commonDir[:marker]
		} else if filepath.Base(commonDir) == ".git" {
			commonDir = filepath.Dir(commonDir)
		}
		if strings.EqualFold(strings.TrimSuffix(filepath.Base(commonDir), ".git"), want) {
			return true
		}
	}
	return false
}

// verifyIntegratedHeads proves that every direct dependency commit is an
// ancestor of the integration task's final HEAD in the repo that owns it.
// repoDirs lists one checkout per repo — the integration worktrees for
// local_directory, or the detected checkouts under the env workdir for
// managed resources. This check is performed by the daemon after the agent
// exits, so prose output cannot bypass the gate.
func verifyIntegratedHeads(ctx context.Context, repoDirs []string, dependencies []OrchestrationGitDependency, state worktreeMergeState) worktreeMergeState {
	state.IntegrationStatus = "missing_heads"
	required := requiredDependencyHeads(dependencies)
	if len(repoDirs) == 0 {
		for _, head := range required {
			if head.sha != "" {
				state.MissingHeadSHAs = append(state.MissingHeadSHAs, head.sha)
			} else {
				state.MissingHeadSHAs = append(state.MissingHeadSHAs, "step:"+head.key)
			}
		}
		return state
	}
	for _, head := range required {
		if head.sha == "" {
			state.MissingHeadSHAs = append(state.MissingHeadSHAs, "step:"+head.key)
			continue
		}
		if integratedInRepos(ctx, repoDirs, head.repo, head.sha) {
			state.IntegratedHeadSHAs = append(state.IntegratedHeadSHAs, head.sha)
			state.IntegratedHeads = append(state.IntegratedHeads, OrchestrationGitHead{Repo: head.repo, HeadSHA: head.sha})
		} else {
			state.MissingHeadSHAs = append(state.MissingHeadSHAs, head.sha)
		}
	}
	if state.Status == "conflicts" {
		state.IntegrationStatus = "conflicts"
		return state
	}
	if state.Status != "clean" {
		state.MissingHeadSHAs = append(state.MissingHeadSHAs, "worktree:"+state.Status)
	}
	if len(state.MissingHeadSHAs) == 0 {
		state.IntegrationStatus = "complete"
	}
	return state
}

// inspectManagedWorktreeMergeState locates every repository checked out on
// demand under an execenv work directory and preflights each task branch
// against its remote default branch. Managed GitHub resources are usually
// placed one directory below workDir by `agora repo checkout`.
func inspectManagedWorktreeMergeState(ctx context.Context, workDir string) worktreeMergeState {
	return inspectManagedWorktreeState(ctx, workDir, false)
}

func inspectManagedWorktreeState(ctx context.Context, workDir string, readOnly bool) worktreeMergeState {
	state := worktreeMergeState{Status: "unavailable"}
	repos, err := detectLocalRepos(ctx, workDir)
	if err != nil || len(repos) == 0 {
		return state
	}
	for _, repo := range repos {
		state.RepoStates = append(state.RepoStates, inspectManagedRepo(ctx, repo, readOnly))
	}
	applyRepoStates(&state)
	return state
}

func inspectManagedRepo(ctx context.Context, repo localRepo, readOnly bool) RepoGitState {
	repoState := RepoGitState{Repo: repo.Name, MergeStatus: "unavailable"}
	dir := repo.SrcPath
	repoState.Branch, _ = gitOutput(ctx, dir, "symbolic-ref", "--short", "-q", "HEAD")
	repoState.HeadSHA, _ = gitOutput(ctx, dir, "rev-parse", "HEAD")
	dirty, _ := gitOutput(ctx, dir, "status", "--porcelain")
	if strings.TrimSpace(dirty) != "" {
		repoState.MergeStatus = "uncommitted"
		return repoState
	}
	if readOnly {
		repoState.BaseSHA = repoState.HeadSHA
		repoState.MergeStatus = "clean"
		return repoState
	}
	target, err := gitOutput(ctx, dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil || target == "" {
		for _, candidate := range []string{"origin/main", "origin/master"} {
			if _, candidateErr := gitOutput(ctx, dir, "rev-parse", "--verify", candidate); candidateErr == nil {
				target = candidate
				break
			}
		}
	}
	if target == "" || repoState.HeadSHA == "" {
		return repoState
	}
	repoState.BaseSHA, _ = gitOutput(ctx, dir, "merge-base", repoState.HeadSHA, target)
	return mergeTreePreflight(ctx, dir, target, repoState)
}

func mergeTreePreflight(ctx context.Context, repo, target string, repoState RepoGitState) RepoGitState {
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "merge-tree", "--write-tree", target, repoState.HeadSHA)
	out, mergeErr := cmd.CombinedOutput()
	if mergeErr == nil {
		repoState.MergeStatus = "clean"
		return repoState
	}
	repoState.MergeStatus = "conflicts"
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		const marker = "Merge conflict in "
		if index := strings.Index(line, marker); index >= 0 {
			name := strings.TrimSpace(line[index+len(marker):])
			if name != "" && !seen[name] {
				seen[name] = true
				repoState.ConflictFiles = append(repoState.ConflictFiles, name)
			}
		}
	}
	sort.Strings(repoState.ConflictFiles)
	return repoState
}

// inspectWorktreeMergeState performs a non-mutating merge-tree preflight of
// every provisioned worktree against its source checkout's current HEAD. The
// developer's branch and working tree are never checked out or modified.
func inspectWorktreeMergeState(ctx context.Context, run *worktreeRun) worktreeMergeState {
	return inspectWorktreeState(ctx, run, false)
}

func inspectWorktreeState(ctx context.Context, run *worktreeRun, readOnly bool) worktreeMergeState {
	state := worktreeMergeState{Status: "unavailable"}
	if run == nil || len(run.worktrees) == 0 {
		return state
	}
	for _, wt := range run.worktrees {
		state.RepoStates = append(state.RepoStates, inspectLocalWorktreeRepo(ctx, wt, readOnly))
	}
	applyRepoStates(&state)
	return state
}

func inspectLocalWorktreeRepo(ctx context.Context, wt provisionedWorktree, readOnly bool) RepoGitState {
	repoState := RepoGitState{Repo: filepath.Base(wt.SrcRepo), Branch: wt.Branch, BaseSHA: wt.BaseSHA, MergeStatus: "unavailable"}
	repoState.HeadSHA, _ = gitOutput(ctx, wt.Path, "rev-parse", "HEAD")
	dirty, _ := gitOutput(ctx, wt.Path, "status", "--porcelain")
	if strings.TrimSpace(dirty) != "" {
		repoState.MergeStatus = "uncommitted"
		return repoState
	}
	if readOnly {
		repoState.MergeStatus = "clean"
		return repoState
	}
	target, err := gitOutput(ctx, wt.SrcRepo, "rev-parse", "HEAD")
	if err != nil || repoState.HeadSHA == "" {
		return repoState
	}
	return mergeTreePreflight(ctx, wt.SrcRepo, target, repoState)
}

// worktreeSidecars are the daemon-written context files that must never be
// committed into the agent's branch (they are runtime scaffolding).
var worktreeSidecars = []string{
	"CLAUDE.md", "AGENTS.md", "GEMINI.md",
	".agent_context/", ".agora/", ".claude/", ".opencode/", ".cursor/", ".pi/", ".agents/",
}

// addWorktree creates one worktree, reusing an existing branch on retry (a
// re-claimed task may have left the branch behind).
func addWorktree(ctx context.Context, srcRepo, target, branch string, log *slog.Logger) error {
	_, err := addWorktreeAt(ctx, srcRepo, target, branch, "HEAD", false)
	return err
}

func addWorktreeAt(ctx context.Context, srcRepo, target, branch, baseRef string, readOnly bool) (string, error) {
	if readOnly {
		if err := gitRun(ctx, srcRepo, "worktree", "add", "--detach", target, baseRef); err != nil {
			return "", fmt.Errorf("git detached worktree add for %q at %s: %w", srcRepo, baseRef, err)
		}
		return "", nil
	}
	if createErr := gitRun(ctx, srcRepo, "worktree", "add", target, "-b", branch, baseRef); createErr != nil {
		// Branch may already exist (re-claim); add the worktree on it.
		if reuseErr := gitRun(ctx, srcRepo, "worktree", "add", target, branch); reuseErr != nil {
			// A machine may move between the CLI and Desktop profiles. Their
			// workspace roots differ, while the issue branch deliberately does
			// not. If the prior profile left that branch checked out in a valid
			// worktree, Git refuses to check it out again. Continue from the
			// prior branch tip on a deterministic root-scoped alias instead of
			// deleting or stealing the other worktree.
			if priorPath, checkedOut := worktreeForBranch(ctx, srcRepo, branch); checkedOut && !sameCleanPath(priorPath, target) {
				alias := collisionSafeWorktreeBranch(branch, target)
				if aliasReuseErr := gitRun(ctx, srcRepo, "worktree", "add", target, alias); aliasReuseErr == nil {
					return alias, nil
				}
				if aliasCreateErr := gitRun(ctx, srcRepo, "worktree", "add", target, "-b", alias, branch); aliasCreateErr == nil {
					return alias, nil
				} else {
					return "", fmt.Errorf("git worktree add for %q: branch %q is already checked out at %q; create root-scoped branch %q: %w", srcRepo, branch, priorPath, alias, aliasCreateErr)
				}
			}
			return "", fmt.Errorf("git worktree add for %q: create branch %q: %v; reuse branch: %w", srcRepo, branch, createErr, reuseErr)
		}
	}
	return branch, nil
}

func worktreeForBranch(ctx context.Context, srcRepo, branch string) (string, bool) {
	out, err := gitOutput(ctx, srcRepo, "worktree", "list", "--porcelain")
	if err != nil || out == "" {
		return "", false
	}
	want := "refs/heads/" + branch
	current := ""
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			current = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "branch ") && strings.TrimSpace(strings.TrimPrefix(line, "branch ")) == want:
			return current, current != ""
		case line == "":
			current = ""
		}
	}
	return "", false
}

func sameCleanPath(a, b string) bool {
	aAbs, aErr := filepath.Abs(a)
	bAbs, bErr := filepath.Abs(b)
	if aErr == nil && bErr == nil {
		return filepath.Clean(aAbs) == filepath.Clean(bAbs)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func collisionSafeWorktreeBranch(branch, target string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(target)))
	return fmt.Sprintf("%s-root-%x", branch, sum[:4])
}

// commitWorktreeChanges commits any real (non-sidecar) agent changes in each
// worktree onto its agent branch so the work persists after the worktree is
// removed — WITHOUT cleaning the worktrees up (the caller still needs them, e.g.
// to inspect the resulting HEAD). Returns a short human summary of what landed
// on which branch (empty when nothing changed). The developer's source checkout
// is never touched — commits go only onto the agent branches. Read-only /
// detached worktrees (no branch) are skipped: nothing is meant to persist.
func persistWorktreeRunChanges(ctx context.Context, run *worktreeRun, agentName string, log *slog.Logger) string {
	if run == nil {
		return ""
	}
	var lines []string
	for _, wt := range run.worktrees {
		if strings.TrimSpace(wt.Branch) == "" {
			continue // detached read-only worktree — never persist
		}
		status, _ := gitOutput(ctx, wt.Path, "status", "--porcelain")
		if strings.TrimSpace(status) == "" {
			continue // agent left this repo untouched
		}
		if err := gitRun(ctx, wt.Path, "add", "-A"); err != nil {
			log.Warn("local_directory: worktree add -A failed", "path", wt.Path, "error", err)
			continue
		}
		// Unstage the daemon's sidecar scaffolding so it never lands in the
		// agent's commit (a linked worktree does not consult a per-worktree
		// exclude file, so we reset explicitly rather than rely on .gitignore).
		resetArgs := append([]string{"reset", "-q", "--"}, worktreeSidecars...)
		_ = gitRun(ctx, wt.Path, resetArgs...)
		// Nothing staged after unstaging sidecars (only scaffolding changed) → skip.
		if diff, _ := gitOutput(ctx, wt.Path, "diff", "--cached", "--name-only"); strings.TrimSpace(diff) == "" {
			continue
		}
		msg := fmt.Sprintf("agent changes (%s)", agentName)
		if err := gitRun(ctx, wt.Path, "-c", "user.email=agent@agora.local", "-c", "user.name="+agentName, "commit", "-m", msg); err != nil {
			log.Warn("local_directory: worktree commit failed", "path", wt.Path, "branch", wt.Branch, "error", err)
			continue
		}
		lines = append(lines, fmt.Sprintf("- `%s`: committed on branch `%s`", filepath.Base(wt.Path), wt.Branch))
	}
	if len(lines) == 0 {
		return ""
	}
	return "### Agent changes (worktree isolation)\n\nYour source checkout was untouched. The agent's work is committed on these branches:\n\n" + strings.Join(lines, "\n") + "\n"
}

// finalizeWorktrees commits any real agent changes onto their branches (via
// commitWorktreeChanges) and then cleans the worktrees up.
func finalizeWorktrees(ctx context.Context, run *worktreeRun, agentName string, log *slog.Logger) string {
	summary := persistWorktreeRunChanges(ctx, run, agentName, log)
	cleanupWorktrees(ctx, run, log)
	return summary
}

// provisionOrReuseWorktrees returns the issue's worktrees, reusing them when
// they already exist under workDir (same issue's earlier task: dev → QA → fix)
// so QA runs in the dev's exact tree, or provisioning fresh otherwise. The
// second return is true when the worktrees were REUSED. workDir is the
// issue-keyed env dir (persists across the issue's tasks).
func provisionOrReuseWorktrees(ctx context.Context, parent, issueKey, workDir string, log *slog.Logger) (*worktreeRun, bool, error) {
	return provisionOrReuseWorktreesAt(ctx, parent, issueKey, workDir, nil, false, log)
}

func provisionOrReuseWorktreesAt(ctx context.Context, parent, issueKey, workDir string, baseRefs []OrchestrationGitHead, readOnly bool, log *slog.Logger) (*worktreeRun, bool, error) {
	repos, err := detectLocalRepos(ctx, parent)
	if err != nil {
		return nil, false, err
	}
	return provisionOrReuseWorktreesFromReposAt(ctx, repos, parent, issueKey, workDir, baseRefs, readOnly, log)
}

func provisionOrReuseWorktreesForRootsAt(ctx context.Context, roots []string, issueKey, workDir string, baseRefs []OrchestrationGitHead, readOnly bool, log *slog.Logger, preferredBranch ...string) (*worktreeRun, bool, error) {
	repos, err := detectLocalReposForRoots(ctx, roots)
	if err != nil {
		return nil, false, err
	}
	return provisionOrReuseWorktreesFromReposAt(ctx, repos, strings.Join(roots, string(os.PathListSeparator)), issueKey, workDir, baseRefs, readOnly, log, preferredBranch...)
}

func provisionOrReuseWorktreesFromReposAt(ctx context.Context, repos []localRepo, parent, issueKey, workDir string, baseRefs []OrchestrationGitHead, readOnly bool, log *slog.Logger, preferredBranch ...string) (*worktreeRun, bool, error) {
	if len(repos) == 0 {
		return nil, false, fmt.Errorf("local_directory %q contains no git repositories", parent)
	}
	single := len(repos) == 1 && repos[0].RelPath == "."

	// Reuse path: every repo's worktree already present + a valid work tree.
	reuse := &worktreeRun{parent: parent}
	allPresent := true
	for _, repo := range repos {
		target := workDir
		if !single {
			target = filepath.Join(workDir, repo.Name)
		}
		if isGitWorkTree(ctx, target) {
			baseSHA := orchestrationBaseRefForRepo(baseRefs, repo.Name)
			if baseSHA == "" {
				sourceHead, _ := gitOutput(ctx, repo.SrcPath, "rev-parse", "HEAD")
				baseSHA, _ = gitOutput(ctx, target, "merge-base", "HEAD", sourceHead)
			}
			// Read the actual branch on reuse. Orchestration continuations keep
			// the original task's worktree/branch while the new dispatch has a
			// different task id, so recomputing from issueKey would report the
			// wrong branch and git evidence.
			branch, _ := gitOutput(ctx, target, "symbolic-ref", "--short", "-q", "HEAD")
			if readOnly {
				branch = ""
			} else if len(preferredBranch) > 0 {
				branch = migrateLegacyIssueBranch(ctx, target, branch, strings.TrimSpace(preferredBranch[0]), log)
			}
			reuse.worktrees = append(reuse.worktrees, provisionedWorktree{
				SrcRepo: repo.SrcPath, Path: target, Branch: branch, BaseSHA: baseSHA,
			})
		} else {
			allPresent = false
			break
		}
	}
	if allPresent && len(reuse.worktrees) == len(repos) {
		log.Info("local_directory: reusing issue worktrees", "issue", issueKey, "count", len(reuse.worktrees))
		touchDir(workDir) // bump mtime so the TTL sweep reflects last use
		return reuse, true, nil
	}

	// Fresh: clear any partial state + stale worktree metadata, then provision.
	if err := os.RemoveAll(workDir); err != nil {
		return nil, false, fmt.Errorf("remove stale worktree directory %q: %w", workDir, err)
	}
	for _, repo := range repos {
		_ = gitRun(ctx, repo.SrcPath, "worktree", "prune")
	}
	run, err := provisionLocalWorktreesFromReposAt(ctx, repos, parent, issueKey, workDir, baseRefs, readOnly, log, preferredBranch...)
	if err == nil {
		touchDir(workDir)
	}
	return run, false, err
}

// migrateLegacyIssueBranch upgrades an existing issue-scoped worktree from
// agent/<opaque ids> to the server-selected feature/<issue> or fix/<issue>
// name without moving HEAD or losing commits. A collision is left untouched:
// preserving an existing branch is safer than stealing another worktree.
func migrateLegacyIssueBranch(ctx context.Context, worktreePath, current, preferred string, log *slog.Logger) string {
	if preferred == "" || current == preferred || !strings.HasPrefix(current, "agent/") {
		return current
	}
	if err := gitRun(ctx, worktreePath, "branch", "-m", preferred); err != nil {
		log.Warn("local_directory: legacy issue branch rename failed; keeping existing branch",
			"path", worktreePath, "branch", current, "preferred_branch", preferred, "error", err)
		return current
	}
	log.Info("local_directory: renamed legacy issue branch", "path", worktreePath, "from", current, "to", preferred)
	return preferred
}

// touchDir bumps a directory's modified time so the idle-TTL sweep treats it as
// recently used (nested writes to worktree files don't update the env dir's
// own mtime).
func touchDir(dir string) {
	now := time.Now()
	_ = os.Chtimes(dir, now, now)
}

// sanitizeResourcesForWorktree drops the source local_path (and daemon_id) from
// the local_directory resource when worktree mode is on, so the agent context
// never advertises the developer's checkout as an editable path. Returns the
// input unchanged when worktreeMode is false. The label is kept for display.
func sanitizeResourcesForWorktree(resources []ProjectResourceData, worktreeMode bool) []ProjectResourceData {
	if !worktreeMode {
		return resources
	}
	out := make([]ProjectResourceData, len(resources))
	copy(out, resources)
	for i := range out {
		if out[i].ResourceType != localDirectoryResourceType {
			continue
		}
		var ref localDirectoryRef
		_ = json.Unmarshal(out[i].ResourceRef, &ref)
		// Keep only the label; the agent works in its cwd worktrees, and the
		// source path must not be reachable via the context.
		safe, _ := json.Marshal(map[string]string{"label": ref.Label, "isolation": "worktree"})
		out[i].ResourceRef = safe
	}
	return out
}

// worktreeEnvDir is the issue-keyed directory that holds an issue's worktrees.
// It lives under {WorkspacesRoot}/{ws}/.worktrees/{issue-short} — OUTSIDE the
// per-task env roots — so it survives task teardown and is reused across the
// issue's dev → QA → fix tasks.
func (d *Daemon) worktreeEnvDir(ws, issueKey string) string {
	return filepath.Join(d.cfg.WorkspacesRoot, ws, ".worktrees", shortID(issueKey))
}

func (d *Daemon) orchestrationWorktreeEnvDir(ws, issueKey, stepID, taskID string) string {
	return filepath.Join(d.worktreeEnvDir(ws, issueKey), "steps", shortID(stepID), shortID(taskID))
}

// sameStepOrchestrationWorkdir validates the server-provided continuation cwd
// before it can influence local worktree provisioning. The claim response marks
// this lineage only after selecting the exact orchestration_step_id, but the
// daemon still confines the path to the expected issue/step directory. This
// prevents an ordinary same-issue fallback (or malformed response) from using a
// user-owned directory to bypass task-scoped orchestration isolation.
func (d *Daemon) sameStepOrchestrationWorkdir(task Task, issueKey string) (string, bool) {
	if !task.PriorSessionSameOrchestrationStep || task.OrchestrationStepID == "" || task.PriorWorkDir == "" {
		return "", false
	}
	stepRoot := filepath.Join(d.worktreeEnvDir(task.WorkspaceID, issueKey), "steps", shortID(task.OrchestrationStepID))
	prior := filepath.Clean(task.PriorWorkDir)
	if filepath.Dir(prior) != filepath.Clean(stepRoot) || filepath.Base(prior) == "." {
		return "", false
	}
	return prior, true
}

// worktreeEnvTTL is how long an idle issue-worktree env is kept before the
// sweep reclaims it. Override with AGORA_WORKTREE_ENV_TTL_HOURS; default 48h.
func worktreeEnvTTL() time.Duration {
	if v := strings.TrimSpace(os.Getenv("AGORA_WORKTREE_ENV_TTL_HOURS")); v != "" {
		if h, err := strconv.Atoi(v); err == nil && h > 0 {
			return time.Duration(h) * time.Hour
		}
	}
	return 48 * time.Hour
}

// sweepWorktreeEnvs reclaims issue-worktree envs that have been idle longer than
// the TTL and are not currently held by a running task. For each stale env it
// removes the git worktrees (deriving each source repo from the worktree's own
// git metadata) and deletes the env directory. Best-effort; called from the GC
// loop. isActive reports whether a path is currently in use by a task.
func (d *Daemon) sweepWorktreeEnvs(ctx context.Context, isActive func(path string) bool, log *slog.Logger) {
	root := d.cfg.WorkspacesRoot
	if root == "" {
		return
	}
	wsDirs, err := os.ReadDir(root)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-worktreeEnvTTL())
	for _, ws := range wsDirs {
		if !ws.IsDir() {
			continue
		}
		base := filepath.Join(root, ws.Name(), ".worktrees")
		envs, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range envs {
			if !e.IsDir() {
				continue
			}
			envDir := filepath.Join(base, e.Name())
			if isActive != nil && isActive(envDir) {
				continue
			}
			info, err := e.Info()
			if err != nil || info.ModTime().After(cutoff) {
				continue
			}
			cleanupWorktreeEnvDir(ctx, envDir, log)
		}
	}
}

// cleanupWorktreeEnvDir removes every git worktree under envDir (each subfolder,
// or envDir itself for a single-repo issue) from its source repo, then deletes
// envDir. The agent branches are kept — they hold the committed work.
func cleanupWorktreeEnvDir(ctx context.Context, envDir string, log *slog.Logger) {
	removed := false
	var worktrees []string
	_ = filepath.WalkDir(envDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || !entry.IsDir() || path == envDir {
			return nil
		}
		if isGitWorkTree(ctx, path) {
			worktrees = append(worktrees, path)
			return filepath.SkipDir
		}
		return nil
	})
	for _, worktree := range worktrees {
		removeOneWorktree(ctx, worktree, log)
		removed = true
	}
	if !removed && isGitWorkTree(ctx, envDir) { // single-repo env
		removeOneWorktree(ctx, envDir, log)
	}
	if err := os.RemoveAll(envDir); err != nil {
		log.Warn("local_directory: remove worktree env failed", "dir", envDir, "error", err)
	}
}

// removeOneWorktree removes a single worktree from its source repo, derived
// from the worktree's git-common-dir.
func removeOneWorktree(ctx context.Context, worktree string, log *slog.Logger) {
	common, err := gitOutput(ctx, worktree, "rev-parse", "--git-common-dir")
	if err != nil || common == "" {
		return
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(worktree, common)
	}
	src := filepath.Dir(common) // .../<repo>/.git → <repo>
	if err := gitRun(ctx, src, "worktree", "remove", "--force", worktree); err != nil {
		log.Warn("local_directory: worktree remove (sweep) failed", "worktree", worktree, "error", err)
	}
	_ = gitRun(ctx, src, "worktree", "prune")
}

// cleanupWorktrees removes every worktree in the run and prunes the parent
// repos' worktree metadata. Best-effort; the agent branches are intentionally
// kept (they hold the agent's commits). Safe to call more than once.
func cleanupWorktrees(ctx context.Context, run *worktreeRun, log *slog.Logger) {
	if run == nil {
		return
	}
	for _, wt := range run.worktrees {
		if err := gitRun(ctx, wt.SrcRepo, "worktree", "remove", "--force", wt.Path); err != nil {
			log.Warn("local_directory: worktree remove failed", "path", wt.Path, "error", err)
		}
	}
	pruned := map[string]bool{}
	for _, wt := range run.worktrees {
		if pruned[wt.SrcRepo] {
			continue
		}
		pruned[wt.SrcRepo] = true
		_ = gitRun(ctx, wt.SrcRepo, "worktree", "prune")
	}
}
