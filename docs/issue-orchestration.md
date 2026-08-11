# Issue orchestration

Agora issue orchestration is a persisted control plane over the existing agent
task queue. It coordinates multiple agents and models without adding a second
runtime protocol.

## Data model

- `orchestration_run` owns the issue-level status, mode, policy, and immutable
  per-repository base commit snapshot.
- `orchestration_step` owns routing, capability, model override, retry budget,
  dependency, approval gate, linked task, and terminal evidence.
- `orchestration_event` is the immutable user-visible audit trail.
- `agent_task_queue.orchestration_step_id` connects execution to coordination.

## State flow

```text
draft → running → all dependency-ready steps (up to max_concurrency)
                         ├─ completed → unblock dependent branches
                         ├─ failed → retry within budget
                         └─ failed → run failed

completed gated step → waiting_approval → human approve → next step
all steps completed → run completed
```

## Routing

Every executable step names an agent and may name an opaque provider model.
The task queue forwards that model to the selected agent runtime. Unlike normal
issue tasks, an explicit orchestration model is not downgraded by cost-tier
labels. `model_routing_mode` controls how those immutable step pins are chosen:

- `pinned` (default) preserves each agent/custom-step model and thinking level;
- `cost` uses frontier planning, efficient implementation/QA, and balanced
  integration/review, unless issue risk signals escalate a step;
- `balanced` uses frontier planning, efficient models for small/mechanical
  implementation, and balanced models for the remaining routine work;
- `intelligence` uses frontier models throughout;
- Cursor runtimes delegate adaptive selection to Cursor's native `auto` model;
  providers without an Agora profile safely preserve the existing agent pin.

The resolver records `mode`, `router_version`, and one decision per routed step
under `orchestration_run.policy.model_routing`. Each decision includes the
provider, exact model/thinking pin, quality tier, human-readable reason, and any
risk/scope signals. Generated plans are fully routed. Explicit model or thinking
pins in custom plans remain authoritative; only unpinned custom steps are routed.
Routing selects within the already assigned agent's runtime; it never silently
changes the worker, provider, credentials, skills, or artifact location.
Squads expose an **Auto models** switch that stores `balanced` as their default
for new squad runs. Resolution order is an explicit run request, then project
default, then squad default, then `pinned`; solo and human runs do not inherit a
squad setting. Switching Auto models off restores `pinned` without rewriting
any agent's configured model.
The first provider profiles are Codex, Claude, direct Gemini, Antigravity, and
Cursor. Default plans then separate responsibility from execution control:

- a directly assigned agent remains the development worker;
- a squad-owned issue resolves `squad.leader_id` as both controller and the
  squad-stage worker;
- an agent in a squad remains the development worker while its squad leader
  controls planning and fallback QA/review;
- an explicit `orchestrator_agent_id` overrides the derived controller;
- QA and review cast metadata override the controller for those stages.

The run row snapshots `owner_type`, `owner_id`, `controller_agent_id`,
`execution_strategy`, and `progression_policy`; the JSON policy retains model
routing decisions and legacy aliases only during the compatibility window. The API emits the snapshot so a
mid-run reassignment cannot silently relabel the accountable owner or
controller. `max_concurrency` defaults to 3 and is capped at 10. Steps declare
`depends_on_keys`; the persisted dependency table supports fan-out and join
barriers. A solo plan gives one agent the complete implementation/verification
outcome. A squad plan integrates member branches first, runs QA and review in
parallel on that integrated result, then waits for both before requesting the
single human merge approval.

Planner output is validated before persistence. Keys are unique, dependencies
and parent links point to earlier nodes, step kinds and capabilities are from
the persisted allow-list, and parallel terminal development branches must join
through one integration node before QA or review. `auto_start=false` persists a
draft proposal; a human can inspect or revise it before starting the run.

Ready work is additionally serialized per agent: two independent branches may
run concurrently only when they have different assigned agents. Under
`progression_policy=manual`, one explicit Continue action may fan out a batch of
independent branches, but completion of that batch pauses before any newly-ready
work or retry is dispatched. `automatic` and `gated` continue automatically,
with declared `approval_required` steps acting as the gates.

For `local_directory` resources in worktree mode, the first worker atomically
pins `base_git_states` on the run. Concurrent workers propose their observed
local HEADs, but the first complete snapshot wins and every worker creates its
branch from those exact commits. The source-path mutex is held only while Git
adds/removes/prunes worktree metadata; model sessions run concurrently in
isolated directories. After integration, QA and review open detached worktrees
at the integration step's exact per-repository HEADs. Those verification steps
are read-only: completion is rejected if a worktree is dirty or HEAD moved.

## Handoff contract

The daemon receives the step title, stage, and instructions in its claim
response. Its turn prompt tells the agent to work only that step, publish
evidence to the shared issue, and leave later stages to the backend. Completion
is the handoff signal; the next task is dispatched only after it lands.
Integration completion also hands off structured per-repository Git state;
prose comments cannot substitute for the commit ancestry and exact-HEAD checks.

## API

- `GET /api/issues/{id}/orchestration`
- `POST /api/issues/{id}/orchestration`
- `POST /api/issues/{id}/orchestration/start`
- `PATCH /api/issues/{id}/orchestration`
- `POST /api/issues/{id}/orchestration/steps/{stepId}/approve`
- `POST /api/issues/{id}/orchestration/steps/{stepId}/retry`
- `POST /api/issues/{id}/orchestration/steps/{stepId}/cancel-branch`

All mutations require a human actor. Agents execute steps but cannot approve
their own gates or rewrite the plan. A controller can recommend a route in its
plan handoff; a human accepts it as a durable edit. Draft edits remain draft
until explicit Start. Draft/running plan revisions can reroute a pending step
only to the ready run controller or a ready, capability-compatible member of
that step's squad. Structural child edits are draft-only and are inserted
before the integration join, which is extended to depend on the new branch.
Each accepted proposal or reroute increments the durable plan revision.

## Human interface contract

The issue page must explain the work at two different levels without repeating
the same information:

- `ExecutionStatusBar` answers the issue-level question: is the run planning,
  working, blocked, waiting for approval, or complete?
- `ActiveWork` in the issue body answers the human question: who is working,
  what outcome do they own, what are they doing now, and what remains?
- `ExecutionDrawer` owns the complete plan, revisions, events, retry history,
  model routing, worktrees, branches, and merge evidence.

`ActiveWork` is the only expanded execution surface in the issue body. It
replaces the current orchestration timeline, current-stage plan card, live
changes feed, and sidebar execution log rather than being added beside them.

### Active work hierarchy

1. `Action required` — approval, conflict, failed branch, or an explicit agent
   question. This is absent when the human has nothing to do.
2. `Working now` — one card per running work unit. A squad can show several
   cards concurrently; a solo run normally shows one.
3. `Waiting` — dependency-blocked or capacity-queued work, collapsed to short
   rows with a human reason such as “Waiting for API implementation”.
4. `Completed` — a collapsed count and the most recent handoff. Full history
   stays in the drawer.

Each running card shows only:

- agent avatar, name, and responsibility;
- work-unit title in outcome language;
- the latest agent-authored `PROGRESS:` sentence;
- completed/total to-do count, with the current to-do when available;
- elapsed time and a stale-update warning;
- a contextual control only when useful (`Cancel branch`, `Retry`, or
  `Respond`).

Raw tool calls, shell commands, file paths, worktree branches, provider model
IDs, exit codes, and orchestration event names are advanced diagnostic data.
They never appear in the default issue-body card.

Run creation is progressive: the default action infers routing from the issue.
`Customize` reveals execution strategy, progression policy, concurrency, and a
`Review the proposed plan before it runs` option. The latter creates a durable
draft proposal instead of dispatching workers immediately. Its execution
drawer exposes versioned worker routing for pending development and
verification steps; Start remains the only action that dispatches the draft.

### Source-of-truth joins

The UI must not infer an orchestrated run from the legacy Dev/QA/Review stage
strip. It joins these existing sources:

- `orchestration_run.steps` supplies ownership, dependency, step status, and
  the linked `task_id`;
- the workspace task snapshot supplies actual queued/running state and timing;
- `taskMessagesOptions(task_id)` supplies `PROGRESS:` and TodoWrite content;
- orchestration events supply immutable history in the drawer.

If progress text has not arrived, use “Starting <work-unit title>”. If a running
task has not emitted progress for the stale threshold, say “No update for …”;
do not pretend it is still actively editing a particular file. A squad lead is
shown as `Coordinating` only while it has a real controller task; leadership is
not itself rendered as fake execution.

## Issue-aware manual test authoring

A manual test case is not valid merely because it has a title. It must be
traceable to the issue and must state an observable expected result. The
current quick form hides criterion linkage under “Add details” and enables Save
with title alone, so unrelated or non-assertive cases can enter the release
gate.

### Authoring flow

1. `Requirement` — select the acceptance criterion or issue behavior the case
   is intended to verify. The QA agent may suggest it, but the human sees the
   exact requirement text.
2. `Test` — give the case a behavior-focused name.
3. `Steps` — each compact row is `Action` plus `Expected result`. At least one
   observable expected result is required. Setup is a separate optional field.
4. `Review with QA` — the QA agent checks the draft against the issue snapshot
   and existing cases. This replaces the premature `Save` action.
5. `Confirm` — show the matched criterion, relevance, reason, duplicates, and
   any suggested rewrite. The human can confirm, edit, apply the suggestion, or
   explicitly add anyway with an audit reason.

The review has four product states:

- `Relevant` — directly verifies a named requirement; confirm is primary.
- `Partial` — related, but the expected outcome or coverage is incomplete.
- `Duplicate` — an existing case already proves the same behavior.
- `Not linked` — no defensible connection to the issue was found.

The agent must return a structured result, not prose hidden in a comment:

```json
{
  "relevance": "relevant | partial | duplicate | unrelated",
  "criterion_ref": "AC2",
  "criterion_text": "The saved value is visible after refresh",
  "confidence": 0.92,
  "reason": "The final step verifies persistence after reload.",
  "missing": ["State which value should remain visible"],
  "duplicate_case_ids": [],
  "suggested_title": "Saved value persists after refresh",
  "suggested_steps": []
}
```

### Persistence and routing

Semantic review is asynchronous and must survive refresh. Store a short-lived
`test_case_draft` with the issue revision, author, draft JSON, review status,
review result, and QA task id. Route `review_test_case_draft` to the QA squad
lead or configured QA reviewer without changing the issue's execution stage.
Only `Confirm` creates the real `test_case` row.

The backend already has the authoritative `issue.acceptance_criteria`, while
`IssueResponse` currently omits it. Add a normalized requirement snapshot for
this flow, for example `[{id:"AC1", text:"…"}]`. Both the reviewer and the UI
must use the same snapshot so a later ticket edit cannot silently change what
the user confirmed.

Fast deterministic validation runs before the agent:

- title is non-empty;
- at least one action exists;
- at least one step or overall result is observable;
- a requirement is selected, or the user explicitly asks the reviewer to find
  one;
- exact and near-duplicate existing cases are flagged.

The model then decides semantic relevance. Low confidence never silently
blocks or silently approves: it becomes `Partial` and asks the human to decide.

## Review decision surface

The Review lens owns one question: **is the reviewed change safe to merge, and
what does the human need to do next?** It is not another execution timeline and
it does not own production deployment.

The current implementation splits that answer across an empty review card, a
decision card, a second merge-readiness rail, a gate-details card, an empty PR
card, and a pointer to the Release page. In the empty state, “Review not run
yet” appears repeatedly. The page must instead render one stateful flow:

1. `Waiting` — name the prerequisite in human language, such as “QA must pass
   before code review can start”. Automatic progression has no redundant Run
   button; gated/manual progression offers the one relevant action.
2. `Reviewing` — reuse the issue-body Active work card for the assigned reviewer
   and its latest progress. Do not introduce a second live-agent widget.
3. `Evidence ready` — one verdict summary, reviewed revision, and blocker count.
   Blocker findings open by default; advisory findings and file/line detail are
   secondary.
4. `Decision required` — one sticky decision bar: `Approve & merge` or
   `Request changes`. It states exactly why approval is disabled.
5. `Merged` or `Changes requested` — show the durable outcome and actor, with
   prior evidence collapsed.

The gate breakdown, PR metadata, commit SHA, and reviewed files remain under a
single `Details` disclosure. Do not render an empty Pull requests section. If
there is no reviewable diff, that fact is the primary state and the action is
to create or connect the PR.

### One issue-level approval

`POST /review-decision {action:"approve"}` and the orchestration `release`
approval currently model the same human decision twice. They must converge on
one issue-level merge gate:

- the review step records agent evidence and never asks for human approval;
- the orchestration release step is the human `Approve & merge` gate;
- approving it atomically verifies the same merge-readiness spine, records the
  actor and reviewed revision, stamps `merge:approved`, dispatches the merge,
  and completes the orchestration step;
- requesting changes records the note, fails/reopens the relevant work branch,
  returns the issue to development, and invalidates the old approval;
- a changed PR head invalidates the decision and requires fresh review.

The UI may expose this single action in the status bar, Active work, or Review
lens, but every placement invokes the same mutation and renders the same state.
The user must never approve once in Review and again in Execution plan.

## Release control surface

The Release page owns the release-level decision: **which immutable set of
approved changes is being shipped, what blocks it, and what happened when it
was deployed?** Per-issue QA evidence, review findings, agent progress, test
suite maintenance, bug triage, and model/worktree details remain on their
own issue or project surfaces.

### Current caveats

- `ReleaseHealthStrip`, `ReleaseCommand`, each sprint readiness ring, and the
  Queue lanes repeat the same ready/blocked counts at different altitudes.
- `Scope locked` is not a real gate; it displays pass when the aggregate has at
  least one issue. There is no persisted scope revision or lock.
- sprint readiness is computed from `qa:*` labels, latest test-case runs, and
  the latest sprint regression only. It does not consume orchestration status,
  integration completeness, review evidence, or the issue merge approval.
- `Ship it` does not create or approve a release. It only reveals a deploy
  panel.
- there is no first-class release record. A successful production deploy emits
  `release:shipped`, but the UI cannot represent a durable draft, approval,
  deploying, failed, or shipped lifecycle.
- sprint deploys are dispatched through the highest-numbered non-cancelled
  issue. Deploy history therefore changes its apparent home when a newer issue
  is attached.
- the workspace-wide Ship view can show several independent sprint cards with
  competing primary actions. A user cannot tell which release target the page
  is controlling.
- Bugs, Test suite, Metrics, Queue, and Ship are grouped under one Release
  header. These are QA operations, not parts of the release decision.

### Target hierarchy

The default Release page controls one selected release target at a time:

1. `Release target` — project, sprint/change set, target environment, ref, and
   version/name. Changing the target changes the entire page scope.
2. `Decision` — one sentence (`Blocked`, `Ready for approval`, `Deploying`,
   `Shipped`, or `Failed`) and one primary action (`Resolve blockers`, `Approve
   release`, `Deploy`, or `Retry deploy`).
3. `Gates` — a compact list backed by real state: scope snapshot, issue work
   complete, every branch integrated, issue QA/review/merge approvals complete,
   release regression green, and production approval.
4. `Blockers` — only actionable issue rows, sorted by the next human action.
   Each row says what is blocked, why, who owns the next action, and links to
   that issue's appropriate lens. It does not embed QA commands or findings.
5. `Included changes` — a collapsed human changelog for the immutable scope.
6. `Deployment` — target, current progress, last outcome, rollback/retry when
   supported, and a collapsed release audit trail.

Remove the global health strip and the decorative release-path stepper. Merge
their useful state into the decision and real gate list. Remove list/board QA
triage from the Release surface; the QA queue remains a separate navigation
destination. Move Bugs, Test suite, and Metrics under QA/Quality navigation or
project settings. Release integration configuration stays in Settings and only
its delivery health appears in release details.

### Release state and issue binding

A durable release needs a first-class aggregate rather than another client-side
rollup:

```text
release_run
  id, workspace_id, project_id, sprint_id?, name/version, ref, environment
  status: draft | blocked | ready | waiting_approval | deploying |
          succeeded | failed | cancelled
  scope_revision, approved_by, approved_at, deployed_at

release_run_issue
  release_run_id, issue_id, orchestration_run_id, reviewed_head_sha,
  integrated_head_sha

release_gate / release_event
  typed gate result and immutable audit history
```

Until approval, scope edits create a new `scope_revision` and recompute gates.
Approval snapshots issue membership and every reviewed/integrated head. A later
issue reassignment does not alter the release owner snapshot; a changed head or
scope invalidates approval. `deploy_event` must reference the release (and
optionally sprint) directly instead of borrowing a representative issue.

Issue and Release surfaces consume the same gates:

- issue orchestration `release` completes when the human approves and the
  reviewed issue branch is merged into the release ref;
- the Release page stays blocked until every scoped issue has completed that
  gate and its expected head is present in the integration ref;
- release regression runs only on that integrated ref;
- production deployment requires release-level human approval and writes the
  durable outcome back to the release and every included issue;
- the issue status bar may say “Included in Release 24 · deploying”, but the
  release detail remains the only place to control the deployment.

This preserves two meaningful human decisions without duplication: approve an
individual reviewed change for merge, then approve an immutable collection of
changes for production deployment.
