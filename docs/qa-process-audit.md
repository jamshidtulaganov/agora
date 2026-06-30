# QA process audit — bottlenecks + multi-agent parallelization

> Source: a 31-agent fan-out audit (5 dimensions → adversarial verify → design synthesis) against the real QA-pipeline code. 25 verified findings, 5 solutions. Run `wpkr1iq3r`.

## The headline trap

`agent.max_concurrent_tasks` defaults to **6**, so QA *looks* 6-way parallel. It isn't: **every QA run for the project deploys + smokes ONE box, ONE `work_dir`, via `git checkout -f -B` with NO lock**. Two concurrent runs force-reset that tree under each other → the smoke serves the *wrong* branch and the verdicts are corrupt. So **safe concurrency today ≈ 1, and raising the cap makes it worse.** Isolating the checkout is the prerequisite for *any* parallelism.

## Bottlenecks (verified)

### A. Per-run latency — one QA run is ~2× slower than needed
- **`qa-lat-1` (critical): double build.** The recipe runs the *full* build+lint+test on the baseline checkout, then the *full* suite again on the branch (`slice_action.go:95-102`). build+test dominates wall-clock, and on the common fully-green branch the entire baseline half is pure overhead — it exists only to attribute pre-existing vs new failures.
- **`qa-lat-2` (high): no worktree → cold rebuild.** Baseline + branch share ONE working tree (sequential `git checkout`), so the branch build can't reuse the baseline's caches. (A daemon worktree facility exists — `cmd_repo.go` — but isn't wired into run_qa.)
- **`qa-lat-3` (medium): no short-circuit.** Smoke (app boot + headless Chromium) and new-test authoring still run even after the branch checks already failed — a verdict already decided.
- **`qa-lat-4` (medium): deploy is synchronous** before the QA comment is created (fresh SSH connect per call).

### B. Throughput — all QA funnels through ONE agent
- **`QA-THRU-01` / `qa-par-01` (critical): single leader.** `maybeRunQAOnInReview` resolves `qaSquadLeader` → exactly **one** agent; the QA roster (QA Tester, Reviewer, …) is never enqueued. So workspace QA throughput = `leader.max_concurrent_tasks / run-duration`. 50 issues in_review → 6 run, 44 queued (minutes each).
- **`qa-dev-2` (high): in_review re-entry re-fires.** The mention dedup ignores *running* tasks, so the normal loop (in_review → tweak → in_review) enqueues a **second full QA** each cycle.

### C. Contention / races — parallel QA is currently UNSAFE
- **`qa-race-1` / `qa-par-03` / `QA-THRU-03` (critical): shared work_dir clobber.** Up to 6 concurrent run_qa tasks (the leader claims 6 different issues) all `checkout -f -B` the **same** `box.WorkDir` — they overwrite each other's tree mid-smoke. No lock anywhere (`performBoxSync` / `buildGitSyncScript`).
- **`qa-race-2` (critical): deploy-before-claim window.** `DeploySprintBranch` checks out the branch *now* (Go control-plane), but the smoke runs later when the daemon claims the task — any other deploy to the same box in between resets the tree.
- **`qa-race-3` (high): last-green lost-update.** `git update-ref refs/sprint/<id>/last-green` is 2-arg (no compare-and-swap), advanced by the LLM with no lock → concurrent scope=task runs last-writer-wins / move it backward.
- **`qa-race-4/5` (high): no box mutual exclusion** — per-task deploy and sprint-end regression can fight over the same box on different branches.

### D. Developer-side friction
- **`qa-dev-1` (high): empty acceptance_criteria.** Importers (Bitrix/Zoho — the primary task source) never write `acceptance_criteria`, so `qaPlanContext` falls back to description/diff → the plan-driven tests degrade to diff-derived (the circularity Feature 2 tried to kill).
- **`qa-dev-3` (high): unpushed branch.** deploy-qa assumes the branch is pushed + current; if not, the fetch fails (swallowed on the sprint path) and the smoke runs against stale code.
- **`qa-dev-5/6` (medium): no diff-size bound; manual run_qa has no dedup** (repeated clicks = repeated full gates).

## The solution — run much more QA in parallel, safely

Ordered; **#1 is the load-bearing prerequisite** (everything else multiplies tasks pointed at the shared tree).

| # | Solution | Effort | Gain |
|---|---|---|---|
| **1** | **Isolate the checkout — `git worktree add <per-task-dest> FETCH_HEAD`** instead of in-place `checkout -f -B`; per-task smoke URL from the subdir. (Interim: PG advisory lock keyed on the box in `performBoxSync`.) | L | Turns each box from 1-concurrent into 3-6 concurrent safe smokes |
| **2** | **Fan auto-QA across ALL ready QA-squad agents** — replace `qaSquadLeader` with `qaSquadAgents` (leader + `ListSquadMembers`, `sliceAgentReady` filter), least-busy assign in `maybeRunQAOnInReview`; raise `max_concurrent_tasks`. | M | ~4× (M agents × cap) instead of one leader |
| **3** | **Box pool per project** — `connectedBoxesForIssue` returns the matching set (the loops already find all; only the resolver early-returns), least-loaded assign. | M | × pool size more |
| **4** | **Parallel test tiers / sharding** — split run_qa into tier-scoped slice actions (QA Tester: build+unit, Lead QA: smoke, Reviewer: diff/tests) fanned to distinct roster members + a leader aggregation task. | L | ~2-3× per issue |
| **5** | **Per-task baseline (kill the single moving last-green ref)** — pass the sprint-tip SHA at dispatch + diff per-task, or move the ref advance to a Go post-verdict step with 3-arg CAS. | M | Removes the hidden sprint-level serializer so 1-4 actually compound |

**Compounding:** #1 (safe) → #2 (~4×) × #3 (×pool) × #4 (~2-3×) = an order-of-magnitude more QA throughput. #5 keeps it from collapsing back to 1 for same-sprint issues.

**Quick latency wins (independent of parallelism):** skip the baseline suite on the green path (only re-run the commands that went red); short-circuit smoke/test-authoring after branch checks fail; worktree for a warm baseline. (`qa-lat-1/2/3` — recipe-only edits in `slice_action.go`.)

**Dev-friction fixes:** backfill `acceptance_criteria` on import (Bitrix/Zoho sync) or derive a checklist from the description; make the auto-QA dedup ignore-running (skip if a QA task is already running for the issue); fail-loud when the branch isn't pushed.

## Recommended sequence
1. **#1 worktree isolation** (unblocks everything; also fixes the correctness races qa-race-1/2/4/5).
2. **#2 fan-across-agents** (biggest throughput jump, M effort).
3. The **quick latency wins** (recipe edits) in parallel.
4. Then **#3 box pool**, **#4 tiers**, **#5 per-task baseline** as the sprint load grows.
