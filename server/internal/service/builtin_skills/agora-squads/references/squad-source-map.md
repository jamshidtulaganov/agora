# Squad Source Map

This file records source evidence for `agora-squads/SKILL.md`.

Use this when the task requires exact source paths, edge-case behavior, tests, or contract verification.

## Object Model

### DB shape

Source:

```text
server/migrations/084_squad.up.sql                # base table: name, description, leader_id, creator_id
server/migrations/085_squad_archive.up.sql        # archived_at, archived_by columns
server/migrations/088_squad_instructions.up.sql   # instructions column
server/pkg/db/queries/squad.sql
packages/core/types/squad.ts
```

Key facts:

- `squad` stores `name`, `description`, `leader_id`, `creator_id` (084), archive
  metadata `archived_at`/`archived_by` (085), and `instructions` (088).
- `squad_member` stores `member_type`, `member_id`, and `role`.
- `member_type` is constrained to `agent` or `member`.
- issue `assignee_type` supports `squad`.

## CLI

Source:

```text
server/cmd/agora/cmd_squad.go
```

Commands:

```bash
agora squad list
agora squad get <squad-id>
agora squad create
agora squad update <squad-id>
agora squad delete <squad-id>
agora squad activity <issue-id> <outcome>

agora squad member list <squad-id>
agora squad member add <squad-id>
agora squad member remove <squad-id>
agora squad member set-role <squad-id>
```

Use `--help` for exact flags before writes.

## Create / Update

Source:

```text
server/internal/handler/squad.go                  # CreateSquad ~200-272, UpdateSquad ~287-364
server/pkg/db/queries/agent.sql                   # GetAgentInWorkspace ~15-17
server/pkg/db/generated/agent.sql.go              # getAgentInWorkspace ~1261
```

Contracts:

- create requires `leader_id` (squad.go:215-218);
- leader must be a workspace agent — both create (squad.go:230-237) and update
  (squad.go:333-338) validate via `GetAgentInWorkspace`;
- archived leader is NOT rejected at create/update: `GetAgentInWorkspace` is
  `WHERE id = $1 AND workspace_id = $2` (agent.sql:15-17) with no archived
  filter, so an archived agent can be set as leader here. Archived-leader fails
  closed later, at routing/dispatch — see the readiness gate (squad.go:945,
  isSquadLeaderReady → service.AgentReadiness at squad.go:1017), assignment
  validation (issue.go:2625-2627), and autopilot admission (autopilot.go:885-891);
- leader is auto-added as member with role `leader` (squad.go:258-263);
- updating `leader_id` auto-adds new leader as member if missing (squad.go:340-347).

## Leader Briefing

Source:

```text
server/internal/handler/squad_briefing.go         # buildSquadLeaderBriefing ~104, buildSquadRoster ~121, renderMemberRow ~169
server/internal/handler/daemon.go                  # briefing injection ~1187, ~1530
```

Contracts:

- squad leader tasks append briefing to leader agent instructions
  (daemon.go:1187, 1530);
- briefing includes operating protocol, roster, and optional instructions
  (squad_briefing.go:104-117);
- `instructions` section appears only when non-empty (squad_briefing.go:110-112);
- archived agent members are skipped from roster (squad_briefing.go:178-179);
- no traced behavior injects `instructions` into every squad member.

## Issue Assignment

Source:

```text
server/internal/handler/issue.go                  # assignee validation ~2614-2632
server/internal/handler/squad.go                   # shouldEnqueueSquadLeaderOnAssign ~990, enqueueSquadLeaderTask ~1027
server/internal/service/task.go
```

Contracts:

- `assignee_type="squad"` routes to `squad.leader_id` (squad.go:1028-1050);
- backlog assignment does not immediately enqueue (squad.go:991-993);
- moving out of backlog can enqueue leader (squad.go:990-994 → isSquadLeaderReady);
- assignee change cancels existing issue tasks first;
- private leader access is checked at assign-time (issue.go:2629-2632) and at
  enqueue-time via `canEnqueueSquadLeader` (squad.go:1037);
- archived squad / archived leader rejected at assign-time (issue.go:2622-2627);
- pending task dedup is applied (squad.go:1042-1048).

## Comment / Mention

Source:

```text
server/internal/handler/comment.go                # comment triggers ~1057-1199, squad mention branch ~1352
server/internal/handler/squad.go                   # enqueueSquadLeaderTask ~986 (assign/backlog paths), lastTaskWasLeader ~915
server/internal/service/task.go                   # EnqueueTaskForSquadLeader
```

Contracts:

- commenting on a squad-assigned issue can wake the leader — the comment path
  computes triggers via `computeCommentAgentTriggers` (comment.go:1124), whose
  assigned-squad branch is `computeAssignedSquadLeaderCommentTrigger`
  (comment.go:1162-1199); the same computation backs the trigger-preview
  endpoint;
- explicit `mention://squad/<id>` resolves squad and adds the leader trigger
  (comment.go:1352-1391);
- squad mention does not fan out to members — enqueue targets `squad.LeaderID`
  only (comment.go:1104-1112, and squad.go:1007 on the assign/backlog paths);
- leader task uses `is_leader_task=true` (via `EnqueueTaskForSquadLeader`);
- leader self-trigger loops are guarded — same-leader / last-task-was-leader
  guards (comment.go:1173-1176, lastTaskWasLeader at squad.go:915) and member
  explicit-mention skip (comment.go:1177-1179).

## Autopilot

Source:

```text
server/internal/service/autopilot.go              # resolveAutopilotLeader ~617-655, dispatch ~88-111
server/internal/handler/autopilot.go              # save-time validateAutopilotAssignee ~845-893
```

Contracts:

- squad autopilot resolves executable agent from `squad.leader_id` —
  `resolveAutopilotLeader` squad branch (autopilot.go:639-651);
- readiness/admission checks target the leader: save-time validation rejects an
  archived squad/leader (handler/autopilot.go:881-891), and dispatch re-runs
  `resolveAutopilotLeader` + `AgentReadiness`;
- archived squad fails closed / skips dispatch — `errSquadArchived`
  (autopilot.go:644-645);
- `create_issue` keeps the issue assigned to the squad (autopilot.go:88-97);
- `run_only` creates task directly for leader (autopilot.go:99-106, dispatch via
  `resolveAutopilotLeader` at autopilot.go:284).

## Child-done Parent Trigger

Source:

```text
server/internal/handler/issue_child_done.go       # dispatchParentAssigneeTrigger ~246, triggerChildDoneSquad ~304
```

Contracts:

- when child issue completes and parent is assigned to squad, parent squad
  leader can be triggered (triggerChildDoneSquad at issue_child_done.go:304);
- routing is leader-only — one `EnqueueTaskForSquadLeader` on the leader, no
  member fan-out (issue_child_done.go:214-216, 344);
- loop guards skip same squad, same effective leader, and shared-leader
  cross-squad cases (issue_child_done.go:229-235, effectiveChildAgentOwner ~367,
  childAssigneeIsSquad ~387).

## Private Leader Access

Source:

```text
server/internal/handler/agent_access.go           # canAccessPrivateAgent ~25-40, canEnqueueSquadLeader ~82-91
server/internal/handler/squad.go                   # enqueueSquadLeaderTask gate ~1037
```

Contracts:

- public leaders pass — `canAccessPrivateAgent` returns true when
  `agent.Visibility != "private"` (agent_access.go:26-28);
- agent-to-agent traffic is allowed — `actorType == "agent"` short-circuits
  (agent_access.go:29-31);
- private leader access for members is limited to owner/admin or agent owner
  (agent_access.go:32-39);
- system triggers are treated like agent triggers for squad leader enqueue:
  `canEnqueueSquadLeader` remaps `actorType == "system"` to `"agent"` before
  delegating to `canAccessPrivateAgent` (agent_access.go:87-90). This is wired
  into `enqueueSquadLeaderTask`, which denies the enqueue when the actor cannot
  access the leader (squad.go:1037).

## QA Squad Leader Routing

Source:

```text
server/internal/handler/slice_action.go   # qaSquadLeader, qaSquadAgents, pickLeastBusyQAAgent,
                                           # maybeRunQAOnInReview (devOrchestrated branch + QA-cast branch),
                                           # maybeRouteToDevLeadOnQAFail, qaFailAutorouteEnabled, autoQAEnabled,
                                           # orchestratorForIssue (the TOTAL orchestrator resolver — renamed
                                           #   from devSquadLeaderForIssue; solo agent = itself),
                                           # castAgentForStage + metaCastQAAgent/metaCastReviewAgent (stage casting),
                                           # pipelineManual + metaPipelineMode + wakeOrchestratorManual (manual mode),
                                           # issueMetadataString helper
server/internal/handler/label.go          # AttachLabel wires maybeRouteToDevLeadOnQAFail alongside
                                           # maybeAutoDocsOnLabel
```

Contracts:

- **QA is off the dev → review path.** `issue.go`'s in_review hook dispatches
  exactly ONE task, `maybeRunQAOnInReview`, and that helper self-gates on
  `autoQAEnabled` (`AGORA_AUTO_QA_ENABLED`, no registry default ⇒ OFF). The hook
  no longer chains `maybeGenTests` / `maybeCompileTestCases` /
  `maybeRunTestsOnInReview` behind it. Those three still exist and still run —
  from the manual slice-action endpoint, from `test_case.go`'s post-write
  trigger, and from `comment.go`'s author→compile→run self-sequencing chain —
  but nothing fires them automatically on a status transition.
- `clearStaleQAGateLabels` runs on EVERY genuine in_review entry, outside the
  QA gate, because it is a DB-only correctness sweep (the diff changed, so the
  previous cycle's verdicts are stale) and must not depend on QA configuration.
- `enforceQAGateBeforeDone` ALSO enforces a RISK-TIER human-sign-off gate when
  `riskTierGateEnforced()` (env AGORA_RISK_TIER_GATE_ENFORCED, default off): a
  CRITICAL-tier issue (issueRiskTier == "critical") can only be moved to done by a
  HUMAN — an agent transition to done, from ANY prior status incl. in_review and
  even with its own qa:pass, is held at in_review. This makes the risk_map's
  "critical → human review mandatory" a real gate (was advisory-only). Own flag,
  independent of the QA gate; fail-open on tier-lookup error; humans never held.
  The gate now takes actorType (UpdateIssue resolves it inline; BatchUpdateIssues
  hoists one resolve before the loop).
- `enforceQAGateBeforeDone` also enforces TEST-ACCURACY when `qaDiscriminationEnforced()`
  (env AGORA_QA_DISCRIMINATION_ENFORCED, default off): a qa:pass no longer reaches
  done unless the issue has a DISCRIMINATING test — one that PASSED on the branch and
  FAILED on the pre-change baseline (HasDiscriminatingRunForIssue, from test_run.
  baseline_status set by run_test_cases). A tautological/happy-path test green on both
  proves nothing and does not count. Fail-open on query error; 'unknown' baseline is neutral.
- `maybeRunQAOnInReview` first computes `trivial := issueQAScopeTrivial(issue)`
  — a low-risk change (`tier:trivial`/`tier:light`/`risk:safe`/`type:docs`
  label, or a small non-zero PR diff; `risk:guarded`/`risk:critical` veto;
  0/0/0 or unknown ⇒ full). When trivial, it does NOT route to the lead
  (`devOrchestrated && !trivial`), its roster is filtered by
  `filterQAAgentsForScope` (drops Security/Designer, never empties), and the
  instruction gets the `qaTrivialCeiling` (gate solo, no fan-out). Same
  filter applied to the gen_test_cases / run_test_cases rosters;
- `maybeRunQAOnInReview` computes `devOrchestrated` before picking a runner:
  true when the issue's assignee is `agent` and that agent belongs to any
  squad (`ListSquadsByMember`), or when the assignee is `squad` directly
  (slice_action.go:706-715);
- when `devOrchestrated`, the runner is `qaSquadLeader` — the workspace squad
  whose name contains "qa" (case-insensitive), leader must be non-archived
  and ready (slice_action.go:597-611, 717-721);
- when not orchestrated, OR no QA squad/leader exists, falls back to the
  pre-existing load-balanced pick across `qaSquadAgents` (leader + agent
  members, each ready) via `pickLeastBusyQAAgent` — fewest in-flight tasks
  wins (slice_action.go:619-680, 722-732);
- `maybeRouteToDevLeadOnQAFail` fires from `AttachLabel` for label name
  `qa:fail` (case-insensitive) and resolves the issue's owner via
  `orchestratorForIssue` — a TOTAL resolver (renamed from
  `devSquadLeaderForIssue`): squad-assigned or squad-member → the squad lead;
  a solo `agent` with no squad → the agent ITSELF; only a `member`/unassigned
  issue → false (no agent orchestrator, manual triage);
- it no longer skips when the orchestrator IS the failing assignee (the old
  self-guard is gone): a solo self-orchestrator, or a directly-assigned lead,
  re-fires WITH the qa:fail feedback comment (the difference from a blind
  retry), bounded by the `qa_fail_autoroute_count` metadata cap
  (`qaFailAutorouteMaxAttempts`, cleared on qa:pass by
  `clearQAFailAutorouteBudget`);
- reassigns via `UpdateIssueAssignee` to the orchestrator, resets status to
  `todo` via `UpdateIssueStatus`, then posts an `@orchestrator` mention comment
  carrying the latest QA evidence summary and re-triggers tasks for it;
- **QA cast:** `maybeRunQAOnInReview` checks `castAgentForStage(issue,
  metaCastQAAgent)` BEFORE the devOrchestrated/roster logic — a valid, ready
  cast agent runs QA and short-circuits the rest; an unset/malformed/unknown
  cast degrades to the default path (`castAgentForStage` returns ok=false);
- **Manual pipeline mode:** `pipelineManual(issue)` reads `metaPipelineMode`
  ("manual", case-insensitive; default false). When true, `maybeRunQAOnInReview`
  and `maybeRunReviewOnQAPass` skip the auto-dispatch and instead call
  `wakeOrchestratorManual` (resolve orchestrator → `@mention` comment →
  `triggerTasksForComment`, no-op if no ready orchestrator), and
  `maybeMergeOnQAPass` suppresses auto-merge. qa:fail routing is unaffected;
- both paths are opt-in: `AGORA_AUTO_QA_ENABLED` (autoQAEnabled,
  slice_action.go:402) gates `maybeRunQAOnInReview`;
  `AGORA_QA_FAIL_AUTOROUTE_ENABLED` (qaFailAutorouteEnabled,
  slice_action.go:502-503) gates the fail-autoroute.
- when `maybeRunQAOnInReview` routes to the QA lead (`routedToLead`), the
  instruction is reframed as a delegation directive so the Opus lead delegates
  the gate to a member instead of executing it (slice_action.go, in
  `maybeRunQAOnInReview` just before the comment is built).

## Auto Code Review on qa:pass (Review stage v2)

Source:

```text
server/internal/handler/review_action.go   # autoReviewEnabled, maybeRunReviewOnQAPass,
                                            # resolveReviewerAgent, reviewGateApplies,
                                            # issueHasKnownPR, reviewDispatchMarker
server/internal/handler/slice_action_templates/run_review.md   # reviewer instruction + review-result schema
server/internal/service/review_evidence.go  # ParseReviewResultBlock, CaptureReviewEvidence (label-first;
                                             # rejects self-review: reviewer==author agent),
                                             # LatestReviewResultForIssue (newest-first, ListRecentCommentsForIssue)
server/internal/service/review_notify.go    # NotifyReviewVerdict (review_failed / review_passed / merge_ready)
server/internal/handler/review_decision.go  # POST /api/issues/{id}/review-decision (RequireHumanActor)
server/internal/handler/review_verdict.go   # GET /api/issues/{id}/review-verdict
server/internal/handler/merge_readiness.go  # reviewGateRequired + requiredGatesWithReview + computeMergeReadiness
                                             # (shared spine; review required only for full tier + a diff + active review)
server/internal/handler/label.go            # AttachLabel — merge:override/merge:approved are human-only (403 for machine actors)
```

Contracts:

- `maybeRunReviewOnQAPass` fires from the same three newly-labeled qa:pass
  call sites as `maybeMergeOnQAPass` (label.go AttachLabel, comment.go
  CaptureQAEvidence hook, qa_override.go), gated by
  `AGORA_AUTO_REVIEW_ENABLED` (config registry, Category "Review",
  default off).
- Guards, in order: flag off → **manual pipeline mode** (`pipelineManual` →
  wake the orchestrator to dispatch review itself, no auto-select) → not
  qa:pass → an existing `review:pass`/`review:fail` label → no known PR
  (metadata `pr_number` or `ListPullRequestsByIssue`) → an in-flight dispatch
  (newest `<!--review-dispatch:auto-->` marker comment with no review-result
  comment after it) → no reviewer distinct from the author agent.
- `resolveReviewerAgent` now checks a per-issue review cast FIRST:
  `castAgentForStage(issue, metaCastReviewAgent)` when it isn't the author →
  then `orchestratorForIssue` (the dev lead / solo self) when not the author →
  least-busy other dev-squad agent → QA squad leader → skip. The
  author-exclusion invariant holds for the cast too (a cast == author is
  ignored).
- Reviewer ≠ author invariant, enforced at THREE layers: `resolveReviewerAgent`
  never returns the issue's assignee agent; the manual slice action
  (`POST /slice-actions {kind:"run_review"}`) skips the assignee/own-agent
  defaults and returns 409 when no distinct reviewer resolves (never dispatches
  to the author); and `CaptureReviewEvidence` REJECTS a verdict whose reviewer
  id equals the issue's author agent (self-review can't mint review:pass). A
  zero/unattributed reviewer id skips the capture-time check (some ingress
  can't attribute authorship).
- `CaptureReviewEvidence` is label-FIRST (the CaptureQAEvidence contract):
  the `review:pass`/`review:fail` label attaches before anything else, the
  opposite label detaches (replace-on-write), the FULL label set is
  broadcast, and the typed inbox notification fires only on a NEW attach.
  There is NO review evidence table — the findings live in the verdict
  comment; `GET /review-verdict` re-parses the newest valid block.
- `clearStaleQAGateLabels` (issue.go) also detaches `review:pass`,
  `review:fail`, and `merge:approved` on a genuine in_review re-entry.
- `maybeMergeOnQAPass` accepts both `qa:pass` and `review:pass` trigger
  labels; when the review gate is required (`reviewGateRequired`: full tier +
  a diff to review + auto-review-enabled-or-a-landed-verdict) it proceeds only
  with BOTH verdicts present. If the labels can't be read it fails CLOSED (no
  auto-merge). When auto-review is off and no manual verdict exists, the review
  gate is advisory — qa:pass alone drives the chain, never a silent stall. The
  human-facing READY note is deduped by `<!-- ready-for-human-merge -->` under
  the per-issue `lockIssueQA` lock.
- `computeMergeReadiness` blocks on a RED gate only. It walks `blockingGates`
  (`{qa, review}`), skips any gate that resolves to `pending`, and reports
  `ready=false` only when one resolved to `fail`. A gate that never ran is not
  reported at all. `ci` is NOT a gate: `ci:pass` is written only by a manually
  fired `run_ci` slice action and nothing auto-dispatches it, so the old
  `required: {ci, qa}` default tier made `Ready` structurally false on every
  issue — the Approve button never enabled and every merge became a hand-merge.
  `reviewTier` now carries only `{name, reviews}`; `reviews` is advisory.
- `POST /review-decision` (human-only): `approve` computes the SAME
  `computeMergeReadiness` spine the GET endpoint uses and 409s with the blocked
  reasons unless ready (`merge:override`
  bypasses); on success it attaches `merge:approved` (#2563eb, broadcasting the
  FULL label set), posts a system comment, and dispatches a member-authored
  `gh pr merge` order to the dev squad leader;
  `request_changes` requires a note, resets status to `in_progress`, and
  @mentions the author agent (fallback: the dev squad leader) — review:fail
  is kept until a re-review replaces it.
- Tests: `server/internal/handler/review_test.go`,
  `server/internal/service/review_evidence_test.go`,
  `server/internal/service/review_notify_test.go`.

## Delegated Sub-task Failure Recovery

Source:

```text
server/internal/handler/slice_action.go   # squadFailureRecoveryEnabled,
                                           # maybeRecoverSquadTaskFailure (near maybeRouteToDevLeadOnQAFail),
                                           # squadFailureRecoveryMarker, maxSquadFailureRecoveries
server/internal/handler/daemon.go          # FailTask handler dispatches
                                           # `go h.maybeRecoverSquadTaskFailure(...)` after recording the failure
```

Contracts:

- gated by `AGORA_SQUAD_FAILURE_RECOVERY_ENABLED` (default off);
- fires from the daemon fail-report path (`FailTask`), detached, after the
  task row is marked failed;
- no-ops when: the task has no issue/agent; the failure reason is a clean
  cancel (`cancelled`/`canceled`/`superseded`); the issue is already
  `in_review`/`done`/`cancelled`; the failing agent is in no squad; the
  failing agent IS the squad leader (self-loop guard); the issue already has a
  pending/running task (`HasPendingTaskForIssue`); or the issue already has
  `maxSquadFailureRecoveries` (=3) recovery markers
  (`squadFailureRecoveryMarker`, counted via `ListCommentsForIssue`);
- otherwise posts an `@leader` comment (authored by the issue's creator
  type/id) carrying the failure reason and re-triggers via
  `triggerTasksForComment` — the comment IS the leader re-wake signal.

## Structural QA Gate

Source:

```text
server/internal/handler/slice_action.go   # qaGateEnforced, issueDevOrchestrated,
                                           # issueHasLabel, enforceQAGateBeforeDone (near qaFailAutorouteEnabled)
server/internal/handler/issue.go          # UpdateIssue status-set (single path) + BatchUpdateIssues
                                           # status-set (batch path) both call enforceQAGateBeforeDone;
                                           # batch path also mirrors the maybeRunQAOnInReview hook
server/internal/handler/squad_briefing.go # squadOperatingProtocol carries the "route through in_review,
                                           # never straight to done" hard rule for every squad leader
```

Contracts:

- `enforceQAGateBeforeDone(ctx, issue, prevStatus, target)` returns
  `(in_review, true)` — redirecting the write — only when `qaGateEnforced()`
  (env `AGORA_QA_GATE_ENFORCED == "true"`) AND target==`done` AND
  prevStatus is neither `done` nor `in_review` AND `issueDevOrchestrated`
  AND NOT `issueHasLabel(qa:pass)`; otherwise returns `(target, false)`
  (no-op passthrough);
- `issueDevOrchestrated` is the shared helper (also used by
  `maybeRunQAOnInReview`): true when the assignee is a squad, or an agent
  in ≥1 squad (`ListSquadsByMember`);
- the redirect is applied at the `req.Status` set point in `UpdateIssue`
  (before the `UpdateIssue` DB write) and at `req.Updates.Status` in
  `BatchUpdateIssues`; because the written status becomes `in_review`, the
  existing prev!=in_review→in_review hook fires and routes to the QA lead;
- the batch path mirrors the single path's in_review hook (stale-label sweep +
  the opt-in `maybeRunQAOnInReview`) so a gate-redirect there behaves
  identically;
- when the gate is OFF (default), there is NO status-transition validation
  anywhere: `UpdateIssue`/`UpdateIssueStatus` accept any→any within the
  status CHECK constraint (issue.sql), and routing through `in_review` is
  purely instruction-driven via `squadOperatingProtocol`.

## Lead Orchestrator / Dynamic Subagent Management

Source:

```text
server/internal/handler/agent.go          # CreateAgent, UpdateAgent, skills set/add handlers
server/internal/handler/daemon.go         # applyIssueCostTier ~1121 (tier:trivial -> haiku,
                                           # tier:light -> sonnet, else unchanged)
server/internal/service/builtin_skills/agora-creating-agents/SKILL.md
```

Contracts:

- an agent's own `mat_` task token authenticates the same `agora agent`
  CLI/API surface a human member uses: the auth middleware sets `X-User-ID`
  from the token's bound `UserID` for agent traffic exactly like it does for
  human sessions (auth middleware, `mat_` branch), so `requireUserID` +
  `workspaceMember` resolve normally — there is no actor-type check in
  `CreateAgent`/`UpdateAgent`/skills set/add that rejects agent callers;
- the only gate `CreateAgent` applies is `canUseRuntimeForAgent(member,
  runtime)` (agent.go:775-778) — a private runtime can only be used by its
  owner or a workspace admin — and this is the SAME check for every caller,
  human or agent; it depends on the role of the member the task token
  resolves to, not on actor type;
- `agent env get`/`set` are the one endpoint pair with an explicit
  actor-type deny (owner/admin-only, agent actors rejected regardless of
  role) per `agora-creating-agents/SKILL.md` — create/update/skills have no
  equivalent deny;
- `applyIssueCostTier` is the ONLY automatic model/thinking-level selection
  in the codebase today, and it only fires for two label values
  (daemon.go:1121); every other model choice — including a leader picking a
  model for a subagent it just created — is a manual `--model` flag, not
  platform-driven.

## Persisted issue orchestration

Source:

```text
server/migrations/160_issue_orchestration.up.sql
server/migrations/169_orchestration_run_base_git_states.up.sql
server/migrations/170_orchestration_step_capability.up.sql
server/pkg/db/queries/orchestration.sql
server/internal/handler/orchestration.go
server/internal/service/task.go              # orchestration terminal callback
server/internal/handler/daemon.go             # authoritative per-step model route
server/internal/daemon/prompt.go              # per-step handoff contract
server/internal/daemon/local_worktree.go      # pinned bases, detached verification worktrees, Git evidence
packages/views/issues/components/issue-execution.tsx # status bar, Active Work, advanced execution drawer
docs/orchestration-worktree-smoke.md                  # real Git branch→integration→QA/review smoke
server/internal/handler/orchestration_squad_selection_test.go # capability proposal/reroute integration test
```

Contracts:

- one issue can have only one active (`draft`, `running`, or
  `waiting_approval`) orchestration run;
- steps are dependency-linked; the backend dispatches every ready step up to
  the run capacity while serializing concurrent work assigned to one agent;
- every step persists a capability; planner output must be an acyclic DAG and
  parallel development branches must converge before QA/review;
- draft runs are plan proposals and edits never auto-start them; pending work
  can be rerouted only to a ready, capability-compatible worker inside the
  controller/step-squad boundary, and every accepted edit creates a plan
  revision event;
- structural `add_child` edits are draft-only, insert development work before
  the pending integration join, and add the new branch as an integration
  dependency so proposal edits cannot create an orphan branch;
- `agent_task_queue.orchestration_step_id` is written in the task INSERT, so a
  fast daemon cannot complete before the task-to-step relationship exists;
- explicit orchestration `model_override` is authoritative and is not replaced
  by issue cost-tier labels;
- orchestration owns retry for linked tasks, preventing the generic task retry
  path from creating a second competing attempt;
- the first local worker atomically pins `orchestration_run.base_git_states`;
  every parallel worktree starts from that immutable per-repository snapshot;
- integration completion records per-repository HEADs and daemon-verified
  ancestry for every dependency commit;
- QA/review worktrees are detached at those exact integration HEADs and the
  server rejects a dirty worktree or a moved HEAD;
- agent completion advances to the next step, exhausted retry budgets fail the
  run, and human approval gates pause it with a durable audit event.

## Tests

Relevant test groups:

```text
server/internal/handler/squad_assign_trigger_test.go
server/internal/handler/squad_comment_trigger_test.go
server/internal/handler/squad_briefing_test.go
server/internal/handler/squad_private_leader_test.go
server/internal/handler/autopilot_private_leader_test.go
server/internal/handler/squad_no_action_test.go
```

Verification command:

```bash
go test ./internal/handler -run 'Test.*Squad|Test.*squad|Test.*Autopilot.*Squad|Test.*ChildDone.*Squad'
```

## Shift-left QA prep (in_progress → gen_test_cases)

- `server/internal/handler/issue.go` fires `maybeGenTests(..., prep=true)` on a genuine `prev != in_progress → in_progress` transition (both the single-update and batch paths), detached goroutine, gated by `AGORA_AUTO_QA_ENABLED`.
- `server/internal/handler/slice_action.go` (`maybeGenTests`) serves both triggers: `prep=true` (dev start — appends the SHIFT-LEFT PREP instruction: no diff, no execution, mandatory Playwright `script` per automatable case, targets the project QA manifest) and `prep=false` (in_review backfill). Idempotent via `CountActiveTestCasesForIssue > 0` skip.
- Net effect: while dev agents implement, the QA suite (cases + compiled scripts) is authored in the background; the `in_review` gate (`maybeRunTestsOnInReview`) only executes it.

## Manual QA slice actions route to the QA lead

- `server/internal/handler/slice_action.go` `resolveSliceActionAgent(...)` takes the slice `kind`; for `isQASliceAction(kind)` (run_qa / gen_test_cases / run_test_cases) with no explicit `agent_id` it resolves to `qaSquadLeader(...)` BEFORE the issue's dev assignee — so a manual "Re-run QA" is owned by QA validation, not the developer under test. Falls through to assignee/own-agent only when there is no ready QA squad leader (setups without a QA squad still work).
- `server/internal/handler/slice_action.go` `maybeRunQAOnInReview(...)` resolves the non-sprint smoke target as dev_apps URL → `localDirectoryQATarget(...)` (a project `local_directory` on an ONLINE daemon, `server/internal/handler/connected_box.go`) → `devBoxSmokeURL(...)` connected box. When the local_directory tier wins it appends `qaLocalDirectoryClause(localPath)` (start-via-`/editor/preview` + smoke `127.0.0.1` + tree-safety worktree baseline) instead of a `SMOKE TARGET` URL. All dev-machine tiers gate on `labs.qa_dev_runtimes`.
- `server/internal/service/dev_runtime_pin.go` `maybePinTaskToDevRuntime(...)` pins the QA task to the dev's runtime; when `GetDevRuntimeForProject` misses it falls back to `localDirectoryRuntimeForProject(...)` → `GetOnlineRuntimeForDaemon` so the task lands on the machine hosting the project's `local_directory`. The local_directory pin is NOT gated on runtime ownership (it is an explicit project→daemon binding), but the labs + `AgentInQASquad` gates still apply.
