# Local Worktree Isolation + Multi-Repo Design

**Date:** 2026-07-09 · **Status:** specified, not built · **Target branch:** sd-platform
**Cloud task scope:** Build + open PR into sd-platform (NO auto merge-to-master, NO auto deploy).

This spec extends the shipped `local_directory` mode (agents work in-place in a
developer's own folder; see `agora-local-directory-mode` memory + Phases 0–4a
on sd-platform) with **worktree isolation** so multiple dev tasks run in
parallel, **QA runs in the dev's exact worktree**, and completed work is
integrated per-repo (sprint squash-merge or a PR into a chosen branch). All
decisions below were confirmed with the product owner (Jamshid) 2026-07-09.

## Confirmed decisions (do not re-litigate)

1. **Isolation unit = the ISSUE, not the task.** A worktree is created for an
   issue and REUSED across that issue's task lifecycle (dev → QA → fix), via the
   existing `prior_work_dir` reuse machinery. So QA sees exactly the dev's
   changes (same context).
2. **Parallel across issues, serial within an issue.** Issue X and issue Y run
   in their own worktrees concurrently. Within issue X, dev → QA → fix serialize
   on X's worktree (per-worktree lock, not a whole-folder lock).
3. **Tool calls run LOCALLY.** The daemon spawns the agent CLI as a local OS
   process with `cwd` = the issue's worktree; every tool call (Edit/Bash/…)
   executes in that worktree on the local machine. local_directory tasks are
   pinned to the daemon that hosts the folder (they can never run in cloud).
4. **Multi-repo local layout = PARENT folder + repo subfolders.** One
   `local_directory` resource points at a parent folder (e.g.
   `~/Projects/sd-bridge/`) containing multiple repo checkouts as subfolders
   (`backend/`, `admin-dashboard/`, `docs/`). The daemon detects which
   subfolders are git work trees.
5. **Which repos a task touches = task-description-driven.** The agent reads the
   issue description and decides which repo subfolder(s) to change. No fixed
   "main repo." Every git-work-tree subfolder is available.
6. **Worktree source = the developer's OWN local checkout** of each touched
   repo (`git worktree add` from the subfolder's local repo), so local remotes,
   credentials, hooks, and network position are preserved. Not a bare-clone
   cache.
7. **Integration = per-repo.** Each touched repo gets its own branch and its own
   integration (own PR / own squash-merge, own target-branch modal). Repos are
   independent.
8. **Integration timing = on qa:pass.** dev done → QA (same worktree) → qa:pass
   → integrate → cleanup. Never before QA passes.
9. **Integration rule:**
   - **Sprint mode ON** → for each touched repo, **squash-merge** the issue's
     branch into the sprint's required branch (the sprint defines the branch
     name; it is required when sprint mode is on).
   - **Sprint mode OFF** → open a **PR** into the repo's target branch (`dev` or
     `main`). The target is chosen by the user via a **modal on the first task
     completion** that needs a PR for that (project, repo); the modal has a
     **"set as default for this repo"** checkbox. If default → persist to
     project settings, subsequent tasks skip the modal. If not → ask again next
     time. The agent NEVER guesses the branch.
10. **Push is gated.** For safety, pushing is opt-in per project; **sd-bridge is
    hard-blocked from any push** (CI/CD → prod risk — see the machine-wide
    push block already in place: `git config --global … pushInsteadOf` for
    `salesdoctor/sd-bridge/`, plus the clone's dead push URL + pre-push hook).

## Data model / config

- `project_resource` local_directory ref gains `isolation: "in_place" | "worktree"`
  (default `in_place` for back-compat). JSONB — no migration.
- Per-repo PR target defaults live in `project.settings.pr_targets`:
  `{ "<repo-basename-or-url-key>": "dev" | "main" | "<branch>" }`. Absent ⇒ ask
  via modal on first completion.
- Reuse: `prior_work_dir` is keyed per (agent, issue) today; extend to per
  (issue, repo) so each repo's worktree is reused across the issue's tasks.

## Backend changes (Go)

1. **Multi-repo detection** (`daemon/local_directory.go`): given the parent
   `local_path`, enumerate immediate subdirectories that are git work trees
   (`git -C <sub> rev-parse --is-inside-work-tree`). Cache the list per task.
   A parent that is itself a single git repo (no repo subfolders) stays the
   single-repo case (back-compat).
2. **Worktree provisioning** (`daemon/execenv/execenv.go` +
   `daemon/local_git.go`): in `isolation:"worktree"` mode, for each repo the
   task will touch, `git -C <repoSubfolder> worktree add
   <envRoot>/work/<repo> -b agent/<issue-short>/<repo> HEAD`. Use the
   `ProvisionWorkDir` hook (designed in the WS2 plan) so execenv stays git-free.
   Worktrees materialize the committed HEAD; the parent's uncommitted state is
   not visible (documented).
3. **Reuse per (issue, repo)**: persist each repo's worktree path in the task
   result; return as `prior_work_dir` (per repo) on the next claim for the same
   issue so dev → QA → fix share the worktree. Reuse resets to the agent branch,
   not `--hard` (keep the dev's changes for QA).
4. **Per-worktree lock**: replace the whole-folder `LocalPathLocker` (worktree
   mode only) with a per-(issue,repo)-worktree lock so different issues run in
   parallel. In-place mode keeps the whole-folder lock.
5. **QA targeting** (`handler/slice_action.go` + `service/dev_runtime_pin.go`):
   the QA task for issue X reuses X's worktrees (via `prior_work_dir`); the
   run_qa smoke/preview runs from the touched repo's worktree (dev's changes).
   Drop the earlier "skip worktree resources" guard — QA now works BECAUSE it
   reuses the dev worktree.
6. **Integration on qa:pass** (`handler/slice_action.go` `maybeMergeOnQAPass`
   family + `handler/connected_box.go` sprint helpers):
   - Sprint ON: per touched repo, squash-merge `agent/<issue>/<repo>` into the
     sprint branch (reuse `SprintBranchFor` + `AGORA_SPRINT_AUTO_MERGE`).
   - Sprint OFF: per touched repo, resolve `project.settings.pr_targets[repo]`.
     If set → open PR into it. If unset → emit a `needs_pr_target` state/event
     for that (issue, repo) that the frontend renders as a modal.
   - Respect the push gate: if push is blocked for the project (sd-bridge),
     stop at the branch, record the intended integration, do NOT push/PR.
7. **GC** (`daemon/gc.go` + `execenv`): `git worktree remove --force` +
   `git worktree prune` against each parent repo before reclaiming the env root;
   never `RemoveAll` a worktree without pruning its metadata. The parent user
   folder is never deleted.
8. **Cleanup / sidecars**: per-worktree sidecar excision (each worktree gets its
   own CLAUDE.md/.agent_context; excised on teardown), same as the shipped
   in-place cleanup.

## Frontend changes (TS)

1. **PR-target modal** (`packages/views` — new `pr-target-dialog.tsx`): shown
   when the backend surfaces a `needs_pr_target` for an (issue, repo). Fields:
   repo name (read-only), branch select (`dev` / `main` / custom), "set as
   default for this repo" checkbox. Submit → PUT project settings (if default) +
   trigger PR creation with the chosen branch. Parse-don't-cast on all response
   bodies (zod + `parseWithFallback`).
2. **Isolation setting**: in the local_directory row of
   `project-resources-section.tsx`, an `in_place | worktree` select
   (desktop-only), enum-drift downgrades to in_place.
3. **Settings → Configs tab** (the in-flight config UI): finish the frontend
   `configs-tab.tsx` (grouped toggles for bool flags, inputs for scalars, masked
   read-only secrets with set/not-set, source badge, save/reset) against the
   already-built `/api/instance-config` endpoints; wire into `settings-page.tsx`
   under the workspace tab group (owner-only). i18n per the repo convention.

## Tests

- Go daemon: multi-repo subfolder detection; worktree-per-(issue,repo) create +
  reuse; parallel two-issue no-interference; QA reuses dev worktree and sees its
  changes; per-repo integration selection (sprint squash vs PR-target); push-gate
  blocks sd-bridge; GC prunes worktree metadata; in-place mode unchanged.
- Go handler: `needs_pr_target` emitted when no default; default persists +
  suppresses the modal next time; secret keys stay non-editable.
- Views: PR-target modal submit (default vs ask-again); configs-tab render +
  save/reset; malformed-response guard.

## Out of scope for this task

- Auto merge-to-master + prod deploy (this task ends at an sd-platform PR).
- Cross-repo LINKED PR-sets (per-repo independent PRs only, per decision 7).
- The `mdt_` scoped daemon token workstream.

## Verification

`make check`-equivalent on the touched Go packages + `pnpm typecheck` + the new
tests. Live-verify (if a runner is available) via a local_directory parent
folder with two repo subfolders and two issues, confirming parallel worktrees.
Never external playwright/chrome — use the co-code embedded browser.
