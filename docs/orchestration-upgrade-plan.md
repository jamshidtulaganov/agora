# Orchestration upgrade plan — the human is the bottleneck

Status: drafted 2026-09-19 · Owner: Jamshid · grounded in the code at
`bed9d320` · external research appendix at the end

## Thesis

A measured Agora cycle is 9s of harness, 154s of agent work, and **24.9 hours
of human wait**. The agent is 0.17% of the wall clock. Everything the roadmap
has been optimising — lean briefs, dep caches, session reuse, the repo context
pack — divides into the 154s. The 24.9h is the product.

That number is not a scheduling problem, and it will not yield to exhortation.
It yields to exactly four moves, and this plan is those four moves:

1. **Remove decisions** that did not need a human (risk-routed gates).
2. **Rank the decisions** that remain, by blast radius, in one queue.
3. **Move the decision to where the human already is** (Telegram, phone, inbox).
4. **Stop agents from burning the wait** — a stuck agent must ask, not grind.

The market research says the same thing from the other side: the loudest
agent-trust complaint is *no escalation hatch*, the scarcest resource is review
capacity ("agents are DDoSing our attention"), and the credible position is
*verified, never autonomous* (`docs/market-research-synthesis.md`, G2/G3/G4).
Those are not three features. They are one object — **the human decision
queue** — seen from three angles.

So: there is exactly one scarce resource in Agora. Every thread below either
removes a decision from the queue, ranks it, relocates it, or keeps an agent
from wasting one.

---

## What is already built (the floor this plan stands on)

Agora is not starting from zero on any of this. The map below is what exists
today; every design in this document is an extension of one of these rows, not
a parallel abstraction.

| Capability | Where it lives | State |
| --- | --- | --- |
| Persisted orchestration DAG (run → steps → deps, plan revisions, nested squads) | `server/migrations/160_issue_orchestration.up.sql`, `161`, `162`, `163`; `server/internal/handler/orchestration.go` (4181 ln) | shipped |
| Execution-level inference (assist/direct/standard/coordinated/controlled) | `server/internal/handler/orchestration_level.go:91` `inferTaskExecutionLevel`, `:162` `resolveTaskExecutionLevel` | shipped |
| Per-issue stage casting (orchestrator pins QA/review agent) | `server/internal/handler/slice_action.go:1128-1129` (`cast_qa_agent_id`, `cast_review_agent_id`), resolver `:1143` `castAgentForStage` | shipped |
| Manual pipeline mode (orchestrator woken at each gate) | `slice_action.go:1135` `metaPipelineMode`, `:1176` `wakeOrchestratorManual` | shipped |
| Two-stage pipeline (dev → review; QA on demand; pending gates never block) | `packages/core/issues/stage.ts:45` `SDLCStage = "dev" \| "review"`; `server/internal/handler/merge_readiness.go:16-22`, `:179` `computeMergeReadiness` | shipped |
| Human approval gate (machine credentials cannot approve) | `server/internal/handler/review_decision.go:49` `CreateReviewDecision`, route-gated `RequireHumanActor` | shipped |
| QA verdict capture + evidence floor + blast-radius sizing | `server/internal/service/qa_evidence.go:458` `CaptureQAEvidence`, `:662` `qaEvidenceFloorGap`, `:731` `qaRequiresVisualEvidence` | shipped |
| QA-fail → dev autoroute with a loop cap | `slice_action.go:1591` `maybeRouteToDevLeadOnQAFail`, cap `:1080` `qaFailAutorouteMaxAttempts = 5` | shipped |
| Review-fail mirror of the same loop | `server/internal/handler/review_outcome.go:37`, `:195` | shipped |
| risk_map (critical/guarded/safe, unknown ⇒ guarded) | `server/internal/handler/project_risk_map.go:11-41`, resolver `:86` `issueRiskTier` | shipped, read-only |
| Cost tiering by issue text → model routing | `server/internal/service/issue_tier.go:65` `classifyIssueTier`; `server/internal/handler/daemon.go:1160` `applyIssueCostTier` | shipped |
| Agent-authored progress contract (TodoWrite plan + `PROGRESS:`) | `server/internal/daemon/execenv/runtime_config.go:593-600`; parser `packages/views/issues/components/live-agent-activity.ts:772` | shipped |
| Living truth (derived status + provenance + staleness) | `server/internal/service/task.go:2116` `postDerivedStatusProvenance`; `packages/views/issues/components/staleness-indicator.tsx`; `AGORA_STALE_*` in `server/internal/config/registry.go:81-83` | shipped |
| Telegram question primitive (agent asks, humans tap, answer attributed) | `server/migrations/180_telegram_question.up.sql`; `server/internal/handler/telegram_agent_api.go:230-300`; CLI `server/cmd/agora/cmd_telegram.go:209` `telegram ask` | shipped |
| Task parked on a human (precedent for a non-failure wait state) | `server/internal/service/task.go:1270` `MarkTaskWaitingLocalDirectory` | shipped |
| Per-instance + per-project config registry with kill switches | `server/internal/config/registry.go`; per-project overrides via `project.settings.config` | shipped |
| Release page (Queue / Ship / Bugs / Suite / Metrics) | `packages/views/qa/components/qa-page.tsx:43-45`, `release-queue.tsx` (711 ln), `release-health-strip.tsx` | shipped |
| Inbox with severity + Telegram DM bridge | `packages/core/types/inbox.ts:3-35`; `server/cmd/server/telegram_push_listeners.go:56` → `telegram_push.go:44` `SendIssueInboxDM` | shipped |

### Two structural facts that constrain every design below

**1. There are two pipeline implementations, and they are mutually exclusive.**
`orchestrationOwnsIssuePipeline` (`orchestration.go:268`) is the first guard in
`maybeRunQAOnInReview` (`slice_action.go:2309`), `maybeRouteToDevLeadOnQAFail`
(`:1592`), `maybeMergeOnQAPass` (`:1876`), `maybeRunReviewOnQAPass`
(`review_action.go:298`) and `maybeRunReviewOnReviewEntry` (`:343`). When a
persisted DAG owns an issue, **every label-triggered reflex is disabled**; the
label/comment machinery is the non-orchestrated path.

Consequence, and it is a hard rule for this plan: **every new object here
attaches to the ISSUE or the TASK, never to a stage.** Escalations, risk tier
and the verification badge must be identical in both worlds or the divergence
compounds. Nothing below adds a stage, a step kind, or a DAG column.

**2. Front end and back end already disagree about what a stage is.**
`packages/core/issues/stage.ts:45` says two (`dev | review`) and explicitly
argues QA/design/deploy are not stages (`:13-33`);
`orchestration.go:254-259` persists five (`plan | dev | qa | review | release`).
`use-stage-pipeline.ts` reconciles by projecting the DAG onto the two-dot strip.
This is debt, it is not this plan's job to pay it, and this plan must not make
it worse. Recorded in the kill list with a direction.

---

# A+B. The one scarce resource: the human decision queue

Threads A (human wait) and B (escalation hatch) are the same build. An
escalation and a review request are two item kinds in one ranked list. Split
into two features they produce two inboxes, which is the failure the research
names ("agents DDoSing our attention") rather than the fix.

## B1 — Escalation as a first-class state

### The finding that makes this urgent

Agora's agents **structurally cannot ask a question**, and this was a deliberate
decision taken for a good local reason that has become wrong at the fleet level.

`server/pkg/agent/claude.go:500-513` builds every Claude invocation as:

```
-p --output-format stream-json --input-format stream-json --verbose
--strict-mcp-config --permission-mode bypassPermissions
--disallowedTools AskUserQuestion
```

with the comment: *"The daemon runs Claude in non-interactive stream-json mode
and has no UI for the prompt to render in… User-facing clarification belongs in
an issue comment instead (GitHub #2588)."* Anthropic's own docs confirm the
tool is unavailable in `-p` mode regardless
([tools reference](https://code.claude.com/docs/en/tools-reference), fetched
2026-09-19), so the flag is defensive, not causal. But "belongs in an issue
comment" is a *convention*, not a state: a comment does not park the run, does
not reach an inbox, does not rank in any queue, and does not stop the agent
from guessing and carrying on.

The out-of-band path that does exist is worse at this timescale.
`agora telegram ask` (`server/cmd/agora/cmd_telegram.go:209`) posts buttons and
**blocks the agent process polling for an answer**, with
`telegramQuestionDefaultTimeout = 10 * time.Minute` and
`telegramQuestionMaxTimeout = 60 * time.Minute`
(`server/internal/handler/telegram_agent_api.go:306-309`). Against a measured
24.9h human wait, a 60-minute blocking ask is a timeout generator that also
pins a runtime slot for an hour. It is the right primitive for "deploy to
staging, yes/no?" when someone is demonstrably watching, and the wrong one for
"I am stuck."

And there is no budget anywhere that would *force* an agent to escalate rather
than grind:

| Budget | Where | Reality |
| --- | --- | --- |
| Wall clock | `server/internal/daemon/config.go:30` | `DefaultAgentTimeout = 0` — **no cap by default**, by design (MUL-3064) |
| Turns | `server/pkg/agent/agent.go:36`, emitted at `claude.go:526` | plumbed end-to-end, **never set** — `execOpts` at `daemon.go:3444` omits `MaxTurns` |
| Spend | — | **does not exist** anywhere in daemon or service |
| Loops | `slice_action.go:1080`, `review_outcome.go:37` | exists, but only for the QA-fail and review-fail autoroutes (5 each) |
| Idle | `config.go:44` | 30m idle watchdog — a *liveness* net, not a budget; a productively-looping agent never trips it |

So the contract today is: unlimited time, unlimited turns, unlimited money, and
no way to ask. That is precisely the Devin-class complaint the research names.

### The design

**`task_escalation` — one table, one open row per issue.**

```sql
-- migration 2xx
CREATE TABLE task_escalation (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id      uuid NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    task_id       uuid REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    agent_id      uuid REFERENCES agent(id) ON DELETE SET NULL,
    kind          text NOT NULL CHECK (kind IN
                    ('question','blocked','budget','permission','risk')),
    prompt        text NOT NULL,          -- "what I need, in one sentence"
    detail        text NOT NULL DEFAULT '', -- what I tried
    options       text[] NOT NULL DEFAULT '{}', -- empty ⇒ free-text answer
    risk_tier     text NOT NULL DEFAULT '',     -- snapshot for queue ranking
    status        text NOT NULL DEFAULT 'open'
                    CHECK (status IN ('open','answered','cancelled')),
    answer        text,
    answered_by   uuid REFERENCES "user"(id) ON DELETE SET NULL,
    answered_at   timestamptz,
    raised_at     timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_task_escalation_open_issue
    ON task_escalation(issue_id) WHERE status = 'open';
```

**No `expires_at`.** This is the deliberate break from `telegram_question`
(migration 180, which needs one because it blocks a process). An escalation
waits for a human for as long as a human takes; the *task* is not waiting, it
has ended. Expiry would re-import the exact failure mode — an agent told
"nobody answered" when the truth is "nobody has looked yet."

**State machine, precisely:**

| # | Event | Where | Effect |
| --- | --- | --- | --- |
| 1 | Agent runs `agora issue escalate <id> --need "…" [--tried "…"] [--option A --option B] [--kind blocked]` | new verb in `server/cmd/agora/cmd_issue.go` (verb table at `:87-231`) | CLI **exits 0 immediately** and prints a sentinel line. It does not poll. |
| 2 | `POST /api/issues/{id}/escalations`, agent-actor only | new `server/internal/handler/escalation.go` | Inserts the row; snapshots `issueRiskTier` (`project_risk_map.go:86`) into `risk_tier`. |
| 3 | The run's task is parked | `MarkTaskWaitingHuman`, modelled byte-for-byte on `service/task.go:1270` `MarkTaskWaitingLocalDirectory` | `agent_task_queue.status = 'waiting_human'`, `wait_reason = prompt`. Not a failure: no retry, no failover, no `qa:*` label change. |
| 4 | Fan-out | `CreateInboxItem` (existing callers `service/qa_notify.go:113`, `review_notify.go:109`) + system comment on the issue + `EventInboxNew` (`service/task.go:3048`) | Inbox type `escalation`, severity `action_required`. Telegram DM rides the existing bridge (`cmd/server/telegram_push_listeners.go:56`) with **zero new plumbing**. |
| 5 | Agent's runtime brief tells it to stop | `execenv/runtime_config.go` — a new `## When You Are Stuck` section beside `## Progress You Show The Human` (`:593`) | "State what you need in one sentence and stop. Do not guess. You will be resumed with the answer." |
| 6 | A human answers | issue-detail escalation card, inbox row, or a Telegram button (reuse the index-not-label callback resolution at `telegram_agent_api.go:284-290`) | `status='answered'`, attributed. |
| 7 | Resume | new `ResumeEscalatedTask` beside `enqueueRerunTask` (`service/task.go:1992`) | Re-enqueues with `prior_session_id` preserved, so `claude.go:531` emits `--resume` and the CLI rehydrates the session instead of re-reading the repo. The answer becomes the prompt. |

**Who may clear it.** A human member (`RequireHumanActor`, the same gate as
`review_decision.go`). Additionally the issue's orchestrator when
`kind = 'question'` **and** the orchestrator was explicitly cast — because a
squad leader answering its own worker's scoping question is the whole point of
having an orchestrator, and the cast is the human's consent to that. Never the
agent that raised it. Never an autopilot.

**Terminate / redirect** is already built and needs only to be reachable from
the escalation card: `CancelTask` (`service/task.go:1018`) and
`terminate-task-confirm-dialog.tsx`. "Redirect" is an answer with free text.

### B2 — The budgets that produce escalations

Budgets without an escalation hatch are just a worse failure. Escalation without
budgets never fires. They ship together or not at all.

| Lever | Change | File |
| --- | --- | --- |
| Turns | Set `ExecOptions.MaxTurns` from the issue tier at dispatch | `server/internal/daemon/daemon.go:3444` (currently omits it) |
| Spend | Add `ExecOptions.MaxBudgetUSD`; emit `--max-budget-usd` for the claude backend | `server/pkg/agent/agent.go:23`, `claude.go:500` |
| Wall clock | Per-tier default replacing the global `0` | `server/internal/daemon/config.go:30`, resolved per task rather than per daemon |
| Loops | Generalise `qa_fail_autoroute_count` into `agent_loop_count` on issue metadata | `slice_action.go:1616-1649` is the pattern to copy |

`--max-budget-usd` is the single most important external finding in this
document. Agora drives agents as CLI subprocesses, so provider prompt-caching is
not a lever — but Claude Code v2.1.217+ enforces a hard dollar ceiling in print
mode and reports `total_cost_usd` in `--output-format json`
([CLI reference](https://code.claude.com/docs/en/cli-reference),
[costs](https://code.claude.com/docs/en/costs), fetched 2026-09-19). That is a
real cost cap available to us today for the majority runtime, for the price of
one struct field and one `append`.

Honest limits, stated rather than hidden: only the claude backend has a budget
flag. Codex, Cursor, Copilot, Gemini and the rest get turns + wall clock only
(`--max-turns` is emitted by claude and codebuddy; `opencode.go:74` warns and
ignores it; `cursor.go:422` does not support it). The per-project and
per-workspace caps in thread F are what cover those runtimes, enforced at claim
time rather than in-process.

**Budget exhaustion must escalate, not retry.** `retryableReasons`
(`service/task.go:1678`) is an explicit allowlist — `runtime_offline`,
`runtime_recovery`, `timeout`, `codex_semantic_inactivity`. Add
`ReasonBudgetExhausted` and deliberately **leave it out** of that map, so
`MaybeRetryFailedTask` (`:1704`) declines it and the failure handler raises a
`kind='budget'` escalation instead. A budget that silently retries is not a
budget.

### B3 — What this is not

- Not a permission prompt. `--permission-mode bypassPermissions` stays.
  Re-introducing per-tool approval into a headless fleet would reproduce the
  24.9h wait at tool granularity.
- Not a replacement for `agora telegram ask`. That verb keeps its job:
  a blocking yes/no where a human is demonstrably present. Its table becomes
  one delivery channel of `task_escalation` rather than a parallel object.
- Not an AI judgement call. Escalation is agent-initiated or
  budget-triggered. No classifier decides that an agent "seems stuck."

## A1 — Risk as a first-class object

Risk is already the pipeline's most load-bearing signal and its least
first-class object. `issueRiskTier` (`project_risk_map.go:86`) drives five
decisions: dev landing mode (`slice_action.go:987`), auto-merge refusal
(`:1966`), the done gate (`:1294`), QA gate depth (`:2152`), and the visual
evidence bar (`qa_evidence.go:739`). It has **no write endpoint** — the file
says so itself (`project_risk_map.go:14-17`) — and the tier is *self-reported*:
the agent classifies its own diff against globs handed to it in the brief
(`:31-33`).

Three changes, in order of value:

1. **Derive the tier server-side from the PR's changed files.** The diff is
   already fetched for cost routing (`applyDiffSizeCostDowngrade`,
   `handler/daemon.go:1223`, thresholds `:1211-1214`) and for the evidence
   floor (`qa_evidence.go:747-758`). Glob-match that file list against the
   risk map at PR-open and stamp `risk:*`. This removes the self-report from a
   safety control — the single highest-integrity change in this document, and
   perhaps 150 lines.
2. **A write endpoint.** `PUT /api/projects/{id}/risk-map`, key-scoped
   `jsonb_set` (the existing `SetProjectSettingKey` used by
   `qa_manifest_capture.go:68` is the pattern; the risk-map file explicitly
   warns about clobbering siblings). Plus a Risk tab in the project settings
   Pipeline panel. Without this, "first-class object" is a slogan.
3. **Distinguish `unclassified` from `safe`.** Today `issueRiskTier` returns
   `""` when a project has no risk map. `""` is currently read as "no opinion,"
   which is correct — but it is one careless `switch` away from being read as
   "safe." Make the absence explicit (`risk_tier: "unclassified"`) at the API
   boundary and give every consumer a `default:` branch, per the enum-drift
   rule in `CLAUDE.md`.

## A2 — The ranked queue

The Release page already has the surface: `qa-page.tsx:43` tabs are
`queue | ship | bugs | suite | metrics`, and `release-queue.tsx` (711 lines) is
the Queue lane. It is a *triage* list today. Make it *the* list.

**Ranking function** (computed on read, never stored — the same discipline
`docs/living-truth-plan.md` applies to staleness: "no stored staleness, nothing
new to go stale itself"):

```
score = risk_weight(tier)          # critical 100, guarded 40, safe 10, unclassified 25
      + kind_weight(item)          # escalation 60, merge_ready 30, qa_fail 25, review_fail 25
      + age_hours * 0.5            # capped at +40
      + stale_bonus                # +15 when living-truth says review_done / idle
```

One list, item kinds: `escalation`, `merge_ready`, `review_failed`,
`qa_failed`. All four already exist as inbox types
(`packages/core/types/inbox.ts:5-35`) except `escalation`. The endpoint is a
sibling of `GET /api/issues/staleness` from the living-truth build — a separate
query, never a join into the hot list path.

**Batched review sessions.** One new view over the ranked queue: N items, a
keyboard pass (`j`/`k`, `a` approve, `r` request changes, `e` answer
escalation), each showing the verified badge (thread D), the diff scope, the
QA evidence, and nothing else. This is not a new pipeline — every action posts
to endpoints that already exist (`review_decision.go:49`, the escalation answer
endpoint from B1). Scope: one file in `packages/views/qa/`, reusing
`review-lens.tsx`'s existing mutations (`:504`, `:541`).

The honest claim for this: it does not make a human decide faster; it removes
the 20 seconds of context reconstruction per item and the tab-hunting between
them. Against 24.9h that is small. It matters because it changes *when* the
human sits down — a 10-item queue that takes four minutes gets done at the
first coffee; four scattered notifications get done tomorrow.

## A3 — Decide from the phone

The Telegram bridge exists end to end: inbox → DM
(`telegram_push_listeners.go:56` → `telegram_push.go:44 SendIssueInboxDM`),
group verdict notices (`telegram_review_notify.go:41`, gated by
`AGORA_TELEGRAM_REVIEW_NOTIFY_ENABLED`, `registry.go:65`), and an answered-
button primitive with attribution and replay-safe index-based callbacks
(`telegram_agent_api.go:284-290`, migration 180).

What is missing is that **none of the Telegram surfaces can act.** They notify
and link back to the web app. Add two button pairs, reusing the existing
callback resolution:

- On an `escalation` DM: the escalation's options, or "Reply to answer".
- On a `merge_ready` DM: **Approve** / **Request changes** →
  `POST /api/issues/{id}/review-decision`. That endpoint is already
  `RequireHumanActor`-gated and a Telegram tap is a human, provided the
  Telegram identity is bound to a workspace member — which it is
  (`telegram_bind.go`).

This is the highest ratio of latency removed to code written in the whole
document. A merge approval that currently waits for someone to open a laptop
becomes a tap in a chat they already have open.

## A4 — The auto-merge lane, and why not to build it

The case for: the one credible "1000 fixes shipped agentically" story in the
corpus ran behind a defined risk taxonomy, and the flag already exists
(`AGORA_SPRINT_AUTO_MERGE`, `registry.go:58`).

The case against, which I find stronger:

1. **The tier is not trustworthy enough yet.** It is self-reported by the agent
   today (A1.1 fixes this, and must land and soak first). More importantly
   `issueRiskTier` returns `""` for every project without a risk map, and
   almost no project has one. An auto-merge lane keyed on "lowest tier" would
   auto-merge every unclassified change in every unconfigured project. That is
   the qa-evidence-floor inversion bug repeated with merge semantics.
2. **"Instant revert" is not available where it would be needed.** Under
   `sprint_mode` (`AGORA_SPRINT_WORKTREE_ENABLED`) everyone shares one sprint
   branch, so a revert is not a single-commit operation and the blast radius of
   a bad auto-merge is the sprint, not the change.
3. **Positioning.** `docs/market-research-synthesis.md` is unambiguous: never
   market autonomous; Height died marketing exactly this. An auto-merge lane is
   the most autonomous-looking thing we could ship, and it buys latency we can
   also buy with A3 at a fraction of the trust cost.

**Decision: do not build an auto-merge lane in this plan.** Build the *one-tap
merge* instead — ranked first in the queue, verified badge visible, one
keystroke or one Telegram tap. Revisit only when all three gates are met:

- (i) risk tier is derived server-side from the diff for 100% of PRs in the
  project, for ≥30 days;
- (ii) rework-after-merge (thread D) is measured and flat for the `safe` tier
  over that window;
- (iii) per-project explicit opt-in, with a named revert path that does not
  depend on sprint mode.

Write those gates into the flag's registry description so the next person to
reach for it reads them.

---

# C. Outcome tickets

The practitioner finding is "assign agents the biggest piece justifiable — the
agent decomposes; micro-tickets are the anti-pattern." Agora's decomposition
machinery already exists and is good: `defaultOrchestrationStepsForExecutionLevel`
(`orchestration.go:1465`), plan revisions (migration 162), nested squads (163),
a real dependency DAG (161), `prepareOrchestrationPlan` (`:344`).

Three specific things block outcome tickets from working on top of it.

### C1 — The level inference reads prose length as scope

`inferTaskExecutionLevel` (`orchestration_level.go:91-160`) starts at score 2
and adds `large_scope` only when the issue body exceeds 1200 characters
(`:105`), plus keyword hits for cross-repo, full-stack, multi-platform. A
two-line outcome ticket — *"Users should be able to export their invoices as
PDF"* — scores minimum and resolves to `assist`/`direct`: `MaxConcurrency: 1`,
`PlanShape: lean` (`:185-187`). The system gives the *least* orchestration to
precisely the ticket that needs the most.

This is not a tuning problem; prose length is the wrong proxy. Fix:

- Add an explicit issue kind. `issue.metadata.ticket_kind = "outcome"`, set by
  the composer, by the `agora issue create --outcome` flag, and by the
  assistant's `create_issue` when the text is an outcome sentence.
- When set, `resolveTaskExecutionLevel` floors the resolved level at
  `standard` and forces `PlanShape` to the full shape with a mandatory `plan`
  step. This is a two-line change at `orchestration_level.go:167-175`, which
  already has the `higherTaskExecutionLevel(resolved, safetyFloor)` machinery.
- Leave the keyword scoring alone. It is validated against a real backlog and
  its comments record why each term is in or out (`issue_tier.go:16-30` makes
  the same argument for the cost tier). Do not widen it.

### C2 — Discovered work must be visibly discovered

Giving an agent a bigger ticket is only safe if the human can see what the
agent decided to do that nobody asked for. Today an orchestrator-added step is
indistinguishable from a planned one.

```sql
ALTER TABLE orchestration_step
  ADD COLUMN origin text NOT NULL DEFAULT 'planned'
    CHECK (origin IN ('planned','discovered'));
```

Written as `discovered` by `queueOrchestrationStepAtomically`
(`orchestration.go:3473`) whenever the step is created after
`run.started_at` — plan revisions already carry the timestamps to decide this
(migration 162). Surfaced as a quiet marker in `orchestration-timeline.tsx` and
in the step list, and as one line in the review card: *"3 of 7 steps were
discovered during the run."*

This is the honest counterweight to bigger tickets, and it is the thing that
lets a reviewer calibrate. It is also cheap: one column, one write-site
condition, one badge.

### C3 — Discovered work that deserves its own ticket

The brief already has a `## Sub-issue Creation` section
(`runtime_config.go:852`). Extend the contract: sub-work the orchestrator
judges to be *out of the outcome's scope* becomes a child issue linked to the
step, inherits the parent's risk tier, and lands in the backlog — not in this
run. Discovered scope creep that stays inside the run must be marked
`discovered`; discovered scope creep that leaves the run must become a ticket.
That is the whole rule.

### C4 — How this composes (nothing else changes)

- Assign-to-squad still triggers decompose+delegate. Unchanged.
- Assign-to-agent is still a solo direct task. Unchanged.
- Casting (`cast_qa_agent_id` / `cast_review_agent_id`) still pins stage agents.
  Unchanged.
- An outcome ticket is an **input shape**, not a pipeline.

### C5 — Resist raising parallelism

Both serious 2025 write-ups on multi-agent coding agree, from opposite
positions: Cognition argues single-threaded linear agents are the default and
that dispersed decisions are the failure mode
([Don't Build Multi-Agents](https://cognition.com/blog/dont-build-multi-agents),
2025-06-12); Anthropic reports multi-agent wins on breadth-first research but
notes *"coding tasks show fewer parallelizable opportunities"* at ~15× the
tokens of a chat turn
([multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system),
2025-06-13). Claude Code's own cost guidance puts agent teams at ~7× a standard
session ([costs](https://code.claude.com/docs/en/costs)).

Agora's `standard` and `direct` levels already default to `MaxConcurrency: 1`
(`orchestration_level.go:185-187`). Outcome tickets will create pressure to
raise that, because a big ticket *looks* parallel. **Keep it at 1 for
`standard`.** One orchestrator that decomposes and runs steps mostly
sequentially beats five workers on one outcome, and it costs a fifth as much.
Parallelism stays where it was earned: `coordinated` and `controlled`, where
`squadParallelBudget` (`orchestration.go:1319`) already bounds it.

---

# D. Verification as the product

"Verified > autonomous" is the positioning. Today the evidence for it is real
but scattered across five surfaces: a `qa:pass` label, a `review:pass` label, a
QA evidence row, a review-result comment, and a merge-readiness computation.
A reviewer has to assemble the claim themselves, which means most of the time
nobody does.

## D1 — One computed verification object

Never stored. Computed on read from signals that already exist, exactly as
living-truth Tier 2 computes staleness.

`GET /api/issues/{id}/verification` →

```json
{
  "level": "verified" | "partial" | "unverified",
  "risk_tier": "critical" | "guarded" | "safe" | "unclassified",
  "signals": [
    {"kind":"qa","state":"pass","at":"…","by":{"type":"agent","id":"…"},
     "evidence":{"commands":7,"screenshots":2,"floor_cleared":true}},
    {"kind":"review","state":"pass","at":"…","by":{"type":"agent","id":"…"},
     "findings":{"blocker":0,"major":1,"minor":3}},
    {"kind":"ci","state":"pass","source_url":"…"},
    {"kind":"human","state":"approved","at":"…","by":{"type":"user","id":"…"}}
  ]
}
```

Sources, all present:

| Signal | Source |
| --- | --- |
| qa | `service/qa_evidence.go:458` capture, `:619` evidence row, floor result at `:506-511` |
| review | `service/review_evidence.go:96` capture, `:193` `LatestReviewResultForIssue`; author≠reviewer enforced at `:117-127` |
| ci | `merge_readiness.go:51` `blockingGates` |
| human | `review_decision.go:134` `recordReleaseApproval`, `merge:approved` label `:31-35` |
| risk | `project_risk_map.go:86` |
| provenance | `service/task.go:2116` `postDerivedStatusProvenance` |

**Levels, precisely:**

- `verified` — a QA pass that cleared the evidence floor, **and** a
  `review:pass` from an actor distinct from the author, **and** either
  tier ∈ {safe, guarded} or (tier = critical **and** a human approved).
- `partial` — some signals present; at least one missing, stale (`qa:stale`,
  `qa_evidence.go:771`) or superseded by a newer commit
  (`qa_state.go:95 ReconcileQAState` already computes this).
- `unverified` — no signals.

`unverified` is not a failure. Most changes in a builder-mode project will sit
there and the UI must render it as neutral, not red. This matters more than it
sounds: an unverified badge that looks like an error trains people to ignore
badges.

Rendered as one chip wherever a change appears — issue header, review lens
(`review-lens.tsx`), release queue row, Telegram merge-ready DM — with a hover
that lists each signal **and who produced it**. Provenance is the product;
"verified" with no attribution is a checkmark emoji.

## D2 — Rework-after-merge is the metric. Throughput is banned.

`docs/market-research-synthesis.md` names merge rate a vanity metric and
agent-throughput dashboards the #1 burnout driver in the 2026 corpus. So:

**Definition.** For a merged PR closing issue X, rework = another PR merged
within N days (default 14) that touches ≥1 of the same files **and** is linked
to X or to a bug issue linked to X. One number per project per window, plus the
same number split by risk tier — because the tier split is what tells you
whether the taxonomy is working, which is exactly the gate A4 needs.

Data exists: PR rows and changed-file counts (`handler/github.go`), issue links,
`server/pkg/db/queries/qa_metrics.sql`. One sqlc query, one card on the Release
page health strip (`release-health-strip.tsx`), one row in
`qa-metrics-view.tsx`.

**Banned, written into the registry description and the CLAUDE.md conventions
if it is not there already:** issues closed per agent; tasks per day; tokens per
agent as a leaderboard; any per-human comparison. The line to hold is *measure
the queue and the money, never the people.*

## D3 — Two default flags currently contradict the story

Worth fixing while we are here, because they make the verification claim false
out of the box:

- `AGORA_AUTO_REVIEW_ENABLED` defaults **off** (`review_action.go:25-27`).
- `AGORA_RISK_TIER_GATE_ENFORCED` defaults **off** (`slice_action.go:1226`).

Together these mean the documented "critical → human review mandatory" invariant
is advisory by default — the code comment at `slice_action.go:1289-1292` says so
outright. Meanwhile `AGORA_QA_FAIL_AUTOROUTE_ENABLED` defaults on. A product
whose positioning is "verified" should default its verification gates on.
Recommend flipping `AGORA_RISK_TIER_GATE_ENFORCED` to default **on** in the same
PR that lands A1.1 (server-derived tiers) — not before, because until the tier
is server-derived, flipping it on would enforce a gate against a self-reported
label.

---

# E. Vibe-coder mode

One engine, two skins. Nothing below forks the pipeline, adds a status, or
adds a code path in the daemon. It is a presentation mode plus a lens default
plus a vocabulary map.

**`project.settings.config.presentation_mode`** ∈ `engineer` (default) |
`builder`. Per-project, because a workspace can hold both a legacy Yii app with
a risk map and a weekend side project; per-project config already exists
(`projectConfigOverrides`).

## E1 — Hidden / renamed / unchanged

**Hidden** — rendered nowhere in builder mode:

| What | Where it renders today |
| --- | --- |
| Branch names, PR numbers, commit SHAs, worktree paths | `issue-repo-section.tsx`, `pull-request-list.tsx`, artifact panels |
| Merge / rebase / cherry-pick language | review lens, merge readiness |
| Runtime, model, agent-internals panels | `artifact-runtime-panels.tsx` |
| Raw execution log, orchestration timeline internals | `execution-log-section.tsx`, `orchestration-timeline.tsx` |
| The QA evidence *command transcript* (the verdict stays) | `qa-evidence-section.tsx` |

**Renamed** — the same object, plain words. One map in the locale files
(en / zh-Hans / ru / uz, `parity.test.ts` stays green):

| engineer | builder |
| --- | --- |
| in_review + PR open | "Ready for you to try" |
| `review:pass` + `qa:pass` | "Checked" |
| Merge | "Publish" |
| Revert | "Undo this change" |
| branch / worktree | "a safe copy" — and preferably not named at all |
| agent task run | "the agent is working" |
| `risk:critical` | "touches money or customer data" |
| escalation | "the agent needs you to decide something" |

**Unchanged — visible in both, deliberately:**

- **Spend.** Per task and per project, in dollars. Every vibe-coding tool in
  the market meters invisibly and every one of them generates the same
  complaint; Bolt/Lovable/Replit all price by credits
  ([Lovable](https://docs.lovable.dev/introduction),
  [Replit](https://docs.replit.com/replitai/agent), fetched 2026-09-19) and the
  research names credit meters a churn driver. Showing a *cap you set* in real
  money, with no currency in between, is the differentiator. Hiding it because
  "non-technical users don't care about cost" is exactly backwards: they are
  the ones who get surprised.
- **The verified badge** and its signal list. Simplified wording, same object.
- **Escalations.** The single most important thing a non-engineer must see, and
  the thing every competitor lacks.
- **The preview.**

## E2 — Preview-first is a lens default, not a page

The machinery exists: `qa-live-browser.tsx`, `editor-browser-pane.tsx`, the
daemon's `browser.go` + `/browser/proxy`, and the live-attach shared Chromium.
`packages/views/issues/lens.ts:51-60` is a literal lens registry with
`?lens=` plumbing.

In builder mode the issue detail's default lens is the preview. That is a
default value in one registry, not a new route, not a new component tree.

## E3 — Checkpoints on the existing git machinery

Vibe-coder tools converge on checkpoint/rollback language and hide git entirely
(Replit documents rolling back "to any previous state"; Lovable keeps Git
integration optional and out of the primary loop; v0 offers "deploy immediately
or open a pull request"). Agora can match that vocabulary without changing its
storage:

- A **checkpoint** is the commit at the end of an agent run.
  `persistWorktreeRunChanges` (`daemon/local_worktree.go:653`) already makes it.
- **"Undo this change"** is a revert commit on the same branch. Never a
  force-push, never a history rewrite, never a reset. This is the one place
  where matching a competitor's *semantics* would be wrong — Replit's
  checkpoints are a snapshot store; ours are git, and a revert that preserves
  history is strictly safer and still reads identically to the user.
- **Builder projects are pinned to per-issue branches.** `sprint_mode`
  (`AGORA_SPRINT_WORKTREE_ENABLED`) puts everyone on one shared branch, which
  makes "undo my last change" ambiguous. Builder mode disables it. Stated as a
  constraint, enforced in config validation.

## E4 — Outcome-ticket-only creation

The builder composer is one field — *"What should be different?"* — plus an
optional screenshot. It sets `ticket_kind = "outcome"` (C1), which floors the
execution level and mandates a plan step. There is no status picker, no
priority picker, no assignee picker; the orchestrator resolves all three. This
is the UI-subtraction rule applied at the entry point, and it is the same
engine.

## E5 — Total scope

One project config value; one lens default; one locale vocabulary map; `hidden`
flags on existing components; one composer variant; one config-validation rule.
Zero new backend endpoints beyond D1's verification read. If the implementation
grows a second pipeline, it has gone wrong.

---

# F. Fleet economics

## F1 — Spend caps at three levels

| Level | Enforcement point | Mechanism |
| --- | --- | --- |
| Task | in-process | `--max-budget-usd` (claude), `--max-turns`, per-tier wall clock (B2) |
| Project | pre-claim | a check in `ClaimTaskForRuntime` (`service/task.go:1119`) against the period's captured usage |
| Workspace | pre-claim | same check, wider scope |

Usage capture already exists: `CaptureTaskUsage` (`service/task.go:253`),
`runtime_usage.sql`, the `/usage` surface, and
`backfill_task_usage_hourly`. The pre-claim check is a read against those
rows, not new accounting.

Registry keys, all `KindString` dollar amounts, **empty default = off**:
`AGORA_TASK_BUDGET_USD_TRIVIAL/LIGHT/MEDIUM/HEAVY`,
`AGORA_PROJECT_BUDGET_USD_MONTH` (ProjectScoped),
`AGORA_WORKSPACE_BUDGET_USD_MONTH`. Fail-open: a cap nobody set must never
wedge a fleet.

When a pre-claim cap is exhausted, the task does **not** sit queued forever —
it raises a `kind='budget'` escalation naming the project and the cap. An
invisible ceiling is indistinguishable from a broken daemon.

**This is a cap, not a meter.** No credits, no AI-gated tier, no currency we
mint. The research anti-goal on credit meters is about a metering *product*;
an owner-set dollar ceiling shown in dollars is the opposite of that and should
be said out loud in the settings copy.

## F2 — The weekly-limit SPOF

Today: `maybeFailoverToFallbackRuntime` (`service/task.go:1793`) fires only on
`ReasonAgentProviderQuotaLimit` / `ReasonAgentProviderCapacityOrRateLimit`
(`:1800-1802`), hops **exactly once** (`:1814-1816`), requires the fallback
runtime to be `online` (`:1818`), and is configured **per agent**
(`agent.fallback_runtime_id`, `handler/agent.go:938-941`). The code's own
comment (`task.go:1784-1786`) states the problem: one account's weekly limit
dead-stops the agent *and the whole squad it sits in*.

Three gaps, in value order:

1. **Pre-emptive degradation.** Add `runtime.degraded_until` and set it when a
   runtime returns a quota error; skip degraded runtimes at claim time for the
   cooldown. Today every agent in the fleet discovers the same weekly limit
   independently, each paying a failed dispatch. This is one column, one
   `UPDATE`, one `WHERE` clause, and it converts an N-failure storm into one.
2. **Deterministic detection.** Failure classification is reason-string based.
   Claude Code emits a structured `system/api_retry` stream event carrying
   `error: "rate_limit"` among a fixed category list
   ([headless](https://code.claude.com/docs/en/headless), fetched 2026-09-19).
   The daemon's stream parser (`server/pkg/agent/claude.go:148`) can classify
   from that event instead of inferring from text.
3. **A chain, not a hop.** Make the fallback a list, and resolve it at squad
   level as well as agent level, so a squad has a fleet-wide fallback rather
   than N independently-configured ones.

## F3 — What operators get to see

| Allowed | Forbidden |
| --- | --- |
| Spend by project, by runtime, by period | Issues closed per agent |
| Open escalations and their age | Tasks or PRs per day |
| Review-queue depth and age | Tokens per agent as a leaderboard |
| Rework-after-merge, split by risk tier | Any per-human comparison |
| Runtime health and degradation | Any "agent productivity" framing |

The rule, and the reason, belong in the code next to the first dashboard query
someone writes: *measure the queue and the money, never the people* — because
the corpus's top-scoring burnout finding is AI used as a management speedup
instrument.

---

# G. Sequencing

Each phase is independently shippable and independently valuable. Dependencies
are real and named; nothing here is a big-bang.

### Phase 0 — Budgets + escalation (1 PR, ~1 week)

The highest leverage per line in this document, and it depends on nothing.

- Migration: `task_escalation` (+ `agent_task_queue` waiting-human status).
- `server/internal/handler/escalation.go` — raise, answer, list.
- `service/task.go` — `MarkTaskWaitingHuman`, `ResumeEscalatedTask`,
  `ReasonBudgetExhausted` **excluded** from `retryableReasons`.
- `server/pkg/agent/agent.go` + `claude.go` — `MaxBudgetUSD`;
  `daemon/daemon.go:3444` — set `MaxTurns` and per-tier timeout.
- `server/cmd/agora/cmd_issue.go` — `agora issue escalate`.
- `execenv/runtime_config.go` — `## When You Are Stuck`.
- Frontend: escalation card on issue detail; `escalation` inbox type; locales ×4.
- Tests: the budget→escalation path; the answer→resume path; an escalation
  raised while a DAG owns the issue must behave identically.

### Phase 1 — The ranked queue (1–2 PRs) · depends on Phase 0

- A1.1 server-derived risk tier from the PR file list; A1.2 risk-map write
  endpoint + settings panel; A1.3 `unclassified` at the API boundary.
- `GET /api/issues/decision-queue` (ranked; separate query, not a join).
- `release-queue.tsx` becomes the ranked queue; batched review session view.
- A3 Telegram action buttons for escalation and merge-ready.

### Phase 2 — Verification (1 PR) · depends on Phase 1 for the tier

- `GET /api/issues/{id}/verification`; the badge component; rework-after-merge
  query + health-strip card.
- Flip `AGORA_RISK_TIER_GATE_ENFORCED` to default on **in this PR, not before**.

### Phase 3 — Outcome tickets (1–2 PRs) · independent of 1 and 2

- `ticket_kind` metadata + execution-level floor
  (`orchestration_level.go:167-175`).
- `orchestration_step.origin` + the discovered marker.
- Sub-issue scope rule in the brief.

### Phase 4 — Builder mode (1–2 PRs) · depends on Phases 0 and 2

Needs escalations (the thing a non-engineer must see) and the verified badge
(the thing that replaces reading a diff). Shipping it earlier ships a skin over
a pipeline that cannot talk to its user.

### Phase 5 — Fleet economics hardening (ongoing)

`runtime.degraded_until` first — it is small and it is the SPOF. Then
deterministic rate-limit classification, then fallback chains.

---

# Kill / revive list

**Kill — the repo context pack A/B.** The byte-thesis already failed its own
measurement gate (4.3% usage vs a 15% bar) and the push-only rebuild sits
unpushed on a branch. External evidence now points the same way: token usage
explains ~80% of performance variance in Anthropic's own agent evaluations
(2025-06-13), and Claude Code's cost guidance is explicitly *shrink context,
move instructions into on-demand skills*
([costs](https://code.claude.com/docs/en/costs)). A push-only context pack is
the opposite move. Do not run the A/B — the hypothesis lost the cheap test and
the expensive test costs weeks. Keep `server/internal/repoindex` only under its
existing 2000-token budget (`repoindex/pack.go:53`) and only for
`coordinated`/`controlled` levels.

**Kill — "Req B: the orchestrator owns stage transitions."** It is half-built
already as `pipeline_mode=manual` (`slice_action.go:1135`), where the
reflexes step back and `wakeOrchestratorManual` (`:1176`) nudges the
orchestrator via an @mention comment. The missing half was going to be real
transition *authority* for an agent. Do not build it. The research is
consistent that users keep the human responsible after delegation and that
"autonomous" is the losing frame; and the escalation state (B1) plus the ranked
queue (A2) give the orchestrator what it actually lacked — a way to stop and
ask — without inventing agent-owned authority nobody asked for. Keep manual
mode as-is.

**Kill — the auto-merge lane**, with the three gates in A4 written into the
flag description.

**Kill (as the escalation path) — `agora telegram ask`.** Keep the verb and the
endpoint for what they are good at (a present human, a 10-minute yes/no), stop
referring to them in the runtime brief as the way to ask for help, and fold
`telegram_question` under `task_escalation` as one delivery channel rather than
a second object.

**Deprioritise — lean-brief-by-tier.** It is the last known item on the speed
cut list and it is real: `buildMetaSkillContent`
(`execenv/runtime_config.go:388`) is ~530 lines and reads no tier at all, while
`leanBriefApplies` (`slice_action.go:851`) only trims *server-appended* context
blocks. Threading a tier through `TaskContextForEnv` (`execenv/execenv.go:77`)
and skipping `### Workflow` (`:737-851`), `## Available Commands` (`:509`) and
`## Skills` (`:857`) for trivial tiers is maybe a day's work. It buys seconds
against a 24.9-hour cycle. Do it when someone is already in that file; never
schedule it.

**Revive — `--max-turns`.** Plumbed through `ExecOptions` and emitted by two
backends, never once set. Phase 0.

**Debt, recorded not paid — the two stage models.** `stage.ts:45` says two
stages; `orchestration.go:254` persists five. Everything in this plan attaches
to the issue or the task specifically so it works in both. The convergence
direction, when someone takes it: the DAG's `qa` and `release` steps are not
stages under the settled two-stage model — `qa` is on-demand and `release` is
the human approval gate. Collapsing them would make the DAG's stage set match
`SDLCStage` and delete `use-stage-pipeline.ts`'s reconciliation layer.

---

# Non-goals

- No new pipeline, no second engine, no builder-mode fork.
- No stage added, renamed, or removed in this plan.
- No agent-owned merge authority; `review_decision.go` stays
  `RequireHumanActor`.
- No per-tool permission prompts in headless runs.
- No agent-throughput metrics of any kind, anywhere.
- No credit meters, no AI-gated tiers, no per-agent seat pricing.
- No AI classifier deciding an agent "seems stuck" — escalation is
  agent-initiated or budget-triggered only.
- No auto-merge until A4's three gates are met.

---

# Appendix A — external sources

All fetched 2026-09-19. Marked ★ where the finding changed a design decision in
this document.

| # | Source | Date / version | What it established |
| --- | --- | --- | --- |
| 1 ★ | [Claude Code — CLI reference](https://code.claude.com/docs/en/cli-reference) | v2.1.217+ for budget enforcement | `--max-budget-usd` is a hard per-run dollar cap in print mode ("Budget limit reached"; subagent spend counts; running background subagents are stopped). `--max-turns` exits with an error at the limit. **Basis for B2/F1** — a real cost cap for a CLI-subprocess architecture. |
| 2 ★ | [Claude Code — Manage costs](https://code.claude.com/docs/en/costs) | current | Agent teams ≈7× a standard session; org-level spend limits and per-user caps; cost-reduction guidance is *shrink context, move instructions to on-demand skills*. **Basis for C5 and the context-pack kill.** |
| 3 ★ | [Claude Code — headless / programmatic](https://code.claude.com/docs/en/headless) | v2.1.259+ for `--permission-prompts` | `--output-format json` carries `total_cost_usd`; `system/api_retry` events carry a fixed `error` category list including `rate_limit`. **Basis for F2.2 deterministic detection.** |
| 4 ★ | [Claude Code — tools reference](https://code.claude.com/docs/en/tools-reference) | current | `AskUserQuestion` cannot be used in non-interactive `-p` mode. **Confirms Agora's `--disallowedTools AskUserQuestion` is defensive, not causal — the hatch must be out-of-band (B1).** |
| 5 | [Claude Code — hooks](https://code.claude.com/docs/en/hooks) | v2.1.196+ fields | `PreToolUse`/`UserPromptSubmit` can block; `Stop`/`SubagentStop` can only inject `additionalContext`; `TeammateIdle`/`TaskCompleted` cannot block. Rules out a hook-based "don't stop until you've asked" enforcement. |
| 6 | [Claude Code — subagents](https://code.claude.com/docs/en/sub-agents) | v2.1.271 notes | `maxTurns` returns a *partial* result marked resumable rather than failing. Shape precedent for B1 step 7 (resume, don't restart). |
| 7 | [Claude Code — agent teams](https://code.claude.com/docs/en/agent-teams) | v2.1.178, experimental | Shared task list, direct messaging, 3–5 teammates recommended, "no nested teams", "lead is fixed". Validates Agora's single-orchestrator shape; cautions against raising squad width. |
| 8 ★ | [Cognition — Don't Build Multi-Agents](https://cognition.com/blog/dont-build-multi-agents) | 2025-06-12 | Single-threaded linear agents as the default; parallel subagents make conflicting implicit decisions. **Basis for C5 (keep `standard` at concurrency 1).** |
| 9 ★ | [Anthropic — multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system) | 2025-06-13 | Orchestrator-worker wins on breadth-first research at ~15× chat tokens; *"coding tasks show fewer parallelizable opportunities"*; token usage explains ~80% of performance variance. **Basis for C5 and the context-pack kill.** |
| 10 ★ | [GitHub Copilot cloud agent](https://docs.github.com/en/copilot/concepts/agents/coding-agent/about-coding-agent) | current | Hard 59-minute session cap that "cannot be extended or bypassed"; draft-PR-by-default handoff; guidance to split complex work into smaller tasks. **Precedent for a non-negotiable wall clock (B2); counter-evidence to C's "biggest piece" — resolved by floor-plus-decompose rather than by micro-tickets.** |
| 11 ★ | [Cursor — cloud agents](https://cursor.com/docs/background-agent) | current ("formerly Background Agents") | Unlimited parallelism, merge-ready PRs with demo artifacts, and *"You'll be asked to set a spend limit when you first start using them"*. **Spend limit as an onboarding step, not a buried setting — adopt in F1's copy.** |
| 12 | [OpenAI Codex cloud](https://learn.chatgpt.com/docs/cloud) | current | Parallel isolated cloud tasks; results as summary+diff reviewed before a PR is opened; entry points from GitHub/GitLab/Linear/Slack. No documented blocked-agent behaviour — the gap is industry-wide. |
| 13 | [Devin — instructing effectively](https://docs.devin.ai/essential-guidelines/instructing-devin-effectively) | current | *"Split tasks into verifiable sub-tasks, and start one Devin session for each"*; *"Devin can get stuck without a clear path or when faced with too many interpretations"*; let Devin test its own work before opening a PR. The stuck-ness is acknowledged and unaddressed. |
| 14 | [Devin — pricing](https://devin.ai/pricing) | current | $0 / $20 Pro / $200 Max; Teams $80 + $40/seat. Overage is *"purchase extra usage… consumed at API pricing"* — no hard cap. Confirms the market has no spend ceiling to copy. |
| 15 ★ | [Replit — Agent](https://docs.replit.com/replitai/agent) | Agent 4 referenced | Checkpoints allow rollback "to any previous state"; confirmation prompts before paid actions; git never mentioned. **Vocabulary model for E3, with git-revert semantics substituted.** |
| 16 ★ | [Lovable — introduction](https://docs.lovable.dev/introduction) | current | Chat + preview + code + version history in one project; *"Plans are priced by credits, not by projects or seats"*; Git integration optional and outside the primary loop. **Basis for E1's "Git optional, not absent" and for showing dollars instead of credits.** |
| 17 | [Lovable — security](https://docs.lovable.dev/features/security) | current | Basic and deep security scans, with the explicit caveat that they "cannot guarantee complete security". Nothing on how a non-engineer verifies correctness — the gap thread D fills. |
| 18 | [v0 — introduction](https://v0.app/docs/introduction) | current | "Real-time preview with visual progress indicators and rich UI feedback for all agent actions"; deploy immediately *or* open a PR. Preview-first plus a graduation path to git — the same shape as E2/E5. |

Internal references used throughout:
`docs/market-research-synthesis.md` (2026-09-19),
`docs/living-truth-plan.md` (2026-09-19),
`docs/assistant-domain-plan.md` (2026-09-19).

---

# Appendix B — the number, restated

| | seconds | share of cycle |
| --- | ---: | ---: |
| Harness overhead | 9 | 0.01% |
| Agent work | 154 | 0.17% |
| Human wait | 89,640 | 99.82% |

Any proposal that does not move the third row is a rounding error. That is the
test every item in this plan had to pass, and it is the test the next proposal
should be held to.
