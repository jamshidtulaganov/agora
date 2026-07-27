---
name: agora-squads
description: "Use when creating, inspecting, updating, assigning, mentioning, or debugging Agora squads. Explains what squads are, squad/member fields, CLI commands, leader routing, issue assignment, comments, mentions, autopilot behavior, leader briefing, the QA-lead/dev-lead orchestrator pattern (sibling leads, dynamic subagent creation, model selection), side effects, and product-gap handling."
user-invocable: false
allowed-tools: Bash(agora *)
---

# Agora Squads

## Quick start

If debugging why a squad did or did not run, inspect first:

```bash
agora issue get <issue-id> --output json
agora squad get <squad-id> --output json
agora squad member list <squad-id> --output json
agora issue comment list <issue-id> --recent 20 --output json
```

If command shape is unclear, check `--help` on the subcommand (`agora squad --help`,
`agora squad member --help`, `agora issue update --help`, `agora issue comment add --help`).

Do not assign, comment, mention, update, delete, or record squad activity just to
test. These can mutate workspace state or trigger agent runs.

## Core model

An Agora squad is a workspace routing and coordination object.

A squad is not an agent and does not run work by itself. Squad-routed work runs
through the squad's `leader_id` agent.

Important consequences:

- assigning an issue to a squad routes to the leader;
- mentioning a squad routes to the leader;
- squad-assigned autopilot resolves to the leader;
- squad members are not automatically fanned out;
- squad `instructions` are leader briefing content, not member prompts.

## CLI

Squad commands:

```bash
agora squad list --output json
agora squad get <squad-id> --output json
agora squad create --name <name> --leader <agent-name-or-id> --output json
agora squad update <squad-id> --instructions "<leader coordination policy>" --output json
agora squad delete <squad-id>
```

Member commands:

```bash
agora squad member list <squad-id> --output json
agora squad member add <squad-id> --member-id <id> --type agent|member --role <role> --output json
agora squad member remove <squad-id> --member-id <id> --type agent|member
agora squad member set-role <squad-id> --member-id <id> --member-type agent|member --role <role> --output json
```

Squad leader evaluation command:

```bash
agora squad activity <issue-id> action|no_action|failed --reason "<why>" --output json
```

`activity` is a write: it records the leader's evaluation decision on an issue.
Use it only when acting as the squad leader after evaluating a trigger.

Issue/comment commands often needed with squads:

```bash
agora issue get <issue-id> --output json
agora issue update <issue-id> --help
agora issue comment list <issue-id> --output json
agora issue comment add <issue-id> --help
```

Prefer `--output json` for reads. Use `--help` before writes.

## Squad fields

- `id` — squad UUID.
- `workspace_id` — workspace the squad belongs to.
- `name` — display name; unique per workspace.
- `description` — human-facing metadata/display text; do not assume runtime
  prompt impact unless source proves a consumer.
- `instructions` — squad-level instructions added to the squad leader briefing,
  not directly injected into every squad member.
- `avatar_url` — optional squad avatar URL.
- `leader_id` — agent ID of the squad leader; the runtime target for
  squad-routed work.
- `creator_id` — creator of the squad.
- `archived_at` / `archived_by` — archive metadata; archived squads are rejected
  by assignment/autopilot routing paths.
- `member_count` — list response count of squad members.
- `member_preview` — list response preview of squad members.

Use `instructions` for leader-facing coordination policy: squad responsibility,
delegation, human escalation, and handoff rules. Members do not receive it.

## Squad member fields

- `member_type` — `agent` or `member`.
- `member_id` — ID of the agent or workspace member.
- `role` — roster role label. Non-empty `role` appears in the leader briefing;
  it does not create scheduling, permissions, or routing behavior.

## Creation and leader membership

Creating a squad requires `leader_id`, which must be a workspace agent.
Create/update only checks that the agent exists, so an archived leader fails
closed later: assignment, autopilot admission, and comment/mention readiness
all reject it before enqueueing work.

On create, the backend adds the leader as a member with role `leader`; updating `leader_id` does the same if needed.

## Leader briefing

For squad leader tasks, Agora appends a squad leader briefing to the leader
agent instructions. The briefing includes:

- Squad Operating Protocol;
- Squad Roster;
- Squad Instructions, only when `instructions` is non-empty.

Roster entries include member name, type, mention markdown, and non-empty role;
archived agents are skipped.

## Issue assignment behavior

Issues can be assigned to squads with:

```text
assignee_type = "squad"
assignee_id = <squad-id>
```

Current behavior:

- assignment routes work to `squad.leader_id`;
- it does not enqueue every squad member;
- assignment while status is `backlog` does not immediately start work;
- moving a squad-assigned issue out of `backlog` can trigger the leader;
- changing assignee cancels existing tasks for the issue before enqueueing the
  new assignee path.

**Design-decomposed sub-issues promote themselves — do NOT touch their status.**
For sub-issues from an approved *design proposal* (`design_plan_index` metadata
and a "Design context" description), the platform promotes waiting `backlog`
siblings when prerequisites finish. The child-done system comment confirms it.
Never flip their status yourself: that can double-promote or start blocked work.
Only work design sub-issues already in `todo`.

Assignment rejects a missing type/id pair, non-existent or archived squad,
archived leader, and an inaccessible private leader.

## Comment and mention behavior

On a squad-assigned issue, a new comment can wake the leader; it never fans out
to members.

Squad mention format:

```md
[@Squad Name](mention://squad/<squad-id>)
```

Current behavior resolves the squad, reads `leader_id`, and enqueues a leader
task with the current comment as trigger. It does not enqueue every member.

## QA squad leader routing

Auto-QA (`in_review`) and QA-fail reassignment prefer squad LEADERS for
squad-orchestrated work. This is the dev/QA lead communication rule, not a
general squad behavior. Two mechanisms:

- **`in_review` → auto `run_qa`, OPT-IN ONLY.** QA is not on the dev → review
  path: an issue entering `in_review` goes to the human who reviews and merges
  it, and no QA agent is summoned unless the project set `AGORA_AUTO_QA_ENABLED`
  (default off). Opted in, exactly ONE task fires — the gate. The old
  four-dispatch fan-out (`run_qa` + `gen_test_cases` + `compile_tests` +
  `run_test_cases`, each routed to a DIFFERENT QA agent and so each cold) is
  gone. Routing when it does fire: a squad assignee (or an agent in any squad)
  goes to the QA squad's LEADER (squad name contains "qa", case-insensitive);
  solo / non-squad assignments fan across the whole QA roster so many
  `in_review` issues run concurrently. A per-issue **QA cast**
  (`cast_qa_agent_id`) overrides all of it (see *Stage casting*).
- **Manual QA actions → QA lead, never the dev.** A manually-fired QA-family
  slice action (`run_qa` / `gen_test_cases` / `run_test_cases`, e.g. the QA
  review page's "Re-run QA") with no explicit agent defaults to the QA squad
  LEADER — not the issue's dev assignee. QA validation is owned by QA, never by
  the developer whose work is under test. Falls back to the assignee/own-agent
  only when the workspace has no ready QA squad leader.
- **`in_progress` → shift-left QA prep (same opt-in flag).** With
  `AGORA_AUTO_QA_ENABLED` on, entering `in_progress` gives a QA agent a
  background `gen_test_cases` task: author the cases AND compile their
  Playwright scripts against the project QA manifest while the dev is still
  implementing — no diff-reading, no execution. Runs PARALLEL to dev, blocks
  nothing, and by `in_review` the suite already exists so the gate only
  EXECUTES it. Idempotent: skipped when the issue already has test cases.
- **`qa:fail` label → auto-reassignment.** The issue is handed back to its
  ORCHESTRATOR (`orchestratorForIssue`, a TOTAL resolver: the squad lead for a
  squad-assigned or squad-member issue, or — for a solo agent with no squad —
  the agent ITSELF), status resets to `todo`, and a comment carrying the QA
  verdict summary is posted with an `@orchestrator` mention. That comment IS
  the QA↔dev communication; it lands in the issue's one shared timeline. The
  orchestrator absorbs its OWN qa:fail too — a solo self-orchestrator, or a
  lead that took the work directly, re-fires WITH the failure feedback (the
  difference from a blind retry), bounded by a `qa_fail_autoroute_count`
  attempt cap (5) that leaves a persistently-failing issue for a human.
  DEFAULT-ON (`AGORA_QA_FAIL_AUTOROUTE_ENABLED` defaults true); only a
  human/member-assigned or unassigned issue has no agent orchestrator and
  keeps manual triage.
- **`qa:pass` label → auto code review (`run_review`).** When
  `AGORA_AUTO_REVIEW_ENABLED` is on and the issue has a known pull request,
  a `run_review` task is dispatched to an INDEPENDENT reviewer — never the
  author agent (the issue's assignee). Reviewer resolution: a per-issue
  **review cast** (`cast_review_agent_id`) when it isn't the author → the dev
  squad leader (the orchestrator) when it isn't the author → the least-busy
  other dev-squad agent → the QA squad leader → skip. The author-exclusion
  invariant holds even for the cast: a cast reviewer equal to the author is
  ignored (an agent never reviews its own diff). The reviewer reads the PR diff (`gh pr diff`)
  and posts a fenced ```review-result``` JSON block
  (`{"verdict":"pass"|"fail","summary","commit_sha","files_reviewed",
  "findings":[{"file","line","severity":"blocker"|"major"|"minor","title",
  "detail"}]}`); the server captures it into the `review:pass`/`review:fail`
  label (replace-on-write, like the QA pair). Verdict is `fail` iff any
  finding is a `blocker`. The reviewer NEVER edits code and NEVER merges —
  a human clicks Approve & merge (POST `/api/issues/{id}/review-decision`,
  human-only), which is what actually orders the squad lead to
  `gh pr merge`. For full-tier issues with a PR, `review` is a required
  merge-readiness gate alongside `ci` and `qa`.

Each is env-gated (`AGORA_AUTO_QA_ENABLED`, `AGORA_QA_FAIL_AUTOROUTE_ENABLED`
— now default TRUE —, `AGORA_AUTO_REVIEW_ENABLED`) and degrades silently when
a squad is missing on one side: a squad dev with no QA squad falls through to
the generic roster pick. Note the qa:fail autoroute NO LONGER needs a squad —
its resolver is total, so a solo dev agent's failed QA now routes back to that
agent itself (a corrective retry with the verdict, capped) instead of the old
"manual triage" no-op; only a human/member-assigned issue is left to a human.
All of these gates are ALSO per-project overridable (see the per-project
config: `settings.config` on the project).

When auto-QA routes to the QA LEAD (dev side orchestrated), the instruction
is framed as a DELEGATION directive: the QA lead is told to hand the actual
gate run to a QA member (executed on a faster model) and own the
qa:pass/qa:fail rollup, rather than run the mechanical gate itself. The lead
orchestrates; a member executes.

### Stage casting and manual pipeline mode

The orchestrator controls WHO runs each stage and WHETHER automation drives it,
both as per-issue metadata (`agora metadata set <id> --key <k> --value <v>` — no
new endpoint; a human sets the same keys from the issue inspector). Inert by
default (absent key = today's behavior); no-ops on a human/unassigned issue.

- **Casting — `cast_qa_agent_id` / `cast_review_agent_id`.** Pin an agent to a
  stage. `maybeRunQAOnInReview` runs QA on the cast ahead of the QA-lead/roster
  logic; `resolveReviewerAgent` checks the review cast first (still author-
  excluded). An unset/malformed/not-ready cast degrades to the default — a
  stale cast never wedges. No dev slot (dev = the assignee, set by delegating).
- **Manual mode — `pipeline_mode`=`manual`** (default `auto`). The auto-QA /
  review / merge reflexes step back; instead each WAKES the orchestrator via an
  @mention that triggers its run, so it dispatches run_qa / run_review itself
  and owns the merge. No stall (no ready orchestrator → silent no-op); qa:fail
  routing UNCHANGED.

**Where the app-under-test comes from (non-sprint QA).** The gate resolves a
smoke target in priority order: the developer's declared `dev_apps` URL
(concrete, already running) → a project `local_directory` on an ONLINE daemon
(the folder lives on the dev's own machine) → a deployed connected box →
project `qa_smoke_cmd`/`qa_smoke_url` → generic auto-detect. All of the
dev-machine tiers are gated by `labs.qa_dev_runtimes` (default off). When a
`local_directory` wins, the QA task is PINNED to that daemon and the
instruction tells the agent the app lives at that path on THIS machine —
start it via the daemon's `/editor/preview` and smoke the returned
`127.0.0.1:<port>`. Critically, the agent must NEVER `git checkout`/`reset`/
`stash` or edit files in the user's folder; the run_qa baseline uses a
throwaway `git worktree add <tmp> <merge-base>` instead. (The daemon also
isolates the run on an `agent/…` branch and restores the user's branch when no
commits were made — so a read-only QA gate leaves the developer's tree
pristine.)

**Trivial changes gate SOLO — no review panel.** A low-risk change (a
`tier:trivial` / `tier:light` / `risk:safe` / `type:docs` label, or a tiny PR
diff) does NOT route to the QA lead and its QA roster excludes specialist
reviewers (Security Reviewer / Designer), so a one-file docs or config change
never spins up a multi-agent panel. The run_qa instruction also carries an
explicit ceiling: gate solo, do not @mention or summon any other agent unless
the diff actually touches security-sensitive code or the UI/design. This only
DOWNGRADES on a reliably-small signal — a `risk:guarded`/`risk:critical` label
or unknown size takes the full lead-delegate path unchanged, so real feature
work is never starved of QA. Documentation-only issues get `tier:trivial`
automatically (the auto-tierer's docs keywords), so they flow through this
solo path without a human tagging them.

Before delegating, the lead is instructed to determine the PROJECT's own
stack and testing tooling itself (read package.json/go.mod/composer.json,
existing test dirs, CI config — never assume) and tell the delegate which
tooling applies: a JS/TS repo with Jest/Vitest gets `npm test`/`vitest run`,
a Go repo gets `go test ./...`, and a monolith with no unit-test layer (a
PHP/Yii1 backend with a mixed jQuery/Vue2/Vue3/Angular frontend, for
example) has no build/test command at all — for that stack the rendered
page IS the contract, so the lead routes to browser-driven verification
against the deployed QA box instead of a nonexistent test suite. This keeps
the tooling decision an explicit judgment call the orchestrator makes per
project, not a hardcoded one-size-fits-all recipe.

### Delegated sub-task failure recovery

A failed agent task posts NO completion signal, so an orchestrated issue
would otherwise wedge silently when a delegated member's task dies (timeout,
idle/startup watchdog, provider crash). Gated by
`AGORA_SQUAD_FAILURE_RECOVERY_ENABLED`, the fail path re-wakes the squad
LEADER with an @-mention comment carrying the failure reason so it can
re-delegate to a different member or handle it. It self-limits: it no-ops for
a clean cancellation, a solo agent (no squad), the leader's own task failing
(no self-loop), an issue that already progressed past dev (a sibling
delegation won), or once it has already fired a small number of times on the
same issue (a member that always fails is left for a human rather than looped
on forever).

### Structural QA gate (in_review is not skippable)

A third, related mechanism enforces the "always in communication" rule
STRUCTURALLY rather than by instruction: when `AGORA_QA_GATE_ENFORCED` is on,
a squad-orchestrated issue **cannot** be moved straight to `done`. A direct
`→done` write (from any actor, agent or human) is rewritten to `→in_review`
when all of these hold: the dev side is squad-orchestrated, the issue does
NOT already carry `qa:pass`, and it isn't already in `in_review`. A dev lead
that "self-approves" its own subordinate's work and jumps to `done` is
therefore routed back for review instead of bypassing it. Once `qa:pass` is
present the `→done` write passes through untouched, so the loop converges.
The gate applies on both the single-issue and board batch-update paths.
When the gate is off (default), status transitions are unvalidated — any
status can be set from any other, and routing through `in_review` is purely
a matter of the leader's instructions.

## Lead Orchestrator pattern

A squad leader is not just a routing target — when a squad is used as the
dev/QA pairing for a body of work, its leader is expected to act as an
**orchestrator**: it personally decides which agent handles a task, which
skills and MCP servers that agent needs, and which model fits the task's
difficulty, and it can create or archive its own subagents to do that. This
section is product guidance for briefing a leader this way; it does not
change squad's routing mechanics above (routing to `leader_id` only).

When the issue uses a persisted orchestration run, workers are independent
agent tasks with separate sessions, branches, and worktrees. The run pins one
immutable repository base before parallel work begins; an integration step
must contain every dependency HEAD, and QA/review verify detached copies of
the exact integrated HEAD without editing them. Planner output persists an
explicit capability per step and is rejected unless it is an acyclic DAG whose
parallel development branches converge before QA/review. A draft run is the
proposal boundary. A controller may recommend routing in its plan handoff; a
human accepts structural proposal edits or reroutes through the orchestration
API. Draft edits stay draft until the explicit Start action. Every accepted
route is readiness- and capability-checked against the run controller or the
step's squad before it becomes a versioned plan revision. Neither controller
nor worker may treat an issue comment as proof that Git handoff or integration
succeeded—the daemon and server verify the commit state.

**Dev lead and QA lead are siblings, not a hierarchy.** Structure work as two
squads per unit of work — one dev squad, one QA squad — each with its own
leader. Neither leader is subordinate to the other. The main rule: **the two
leads must always be in communication.** In practice this means every
handoff between dev and QA happens through an `@mention` comment on the
shared issue (see *QA squad leader routing* above for the two automated
paths) — never a silent status change with no comment, on either side. If a
leader needs to hand work to its counterpart for a reason the automation
doesn't already cover, it should mention the other squad
(`[@Squad Name](mention://squad/<squad-id>)`) directly rather than escalate
to a human.

**Every task goes to an orchestrator, not a bare agent.** When wiring up a
squad-based workflow, assign issues with `assignee_type="squad"` (routes to
`leader_id`) rather than `assignee_type="agent"` pointed at an individual —
even if today only one agent exists to do the work. This keeps the leader in
the loop on every task by construction, so it can decide delegation instead
of being bypassed.

**Leaders create and archive their own subagents at runtime.** A running
agent's task-token authenticates the same `agora agent` CLI a human uses —
there is no separate "leader" capability tier. A leader can:

```bash
agora agent create --name <name> --runtime-id <runtime-id> \
  --description "<catalog summary>" --instructions "<runtime contract>" \
  --model <model> --output json
agora agent skills set <agent-id> --skill-ids <id1>,<id2> --output json
agora squad member add <squad-id> --member-id <new-agent-id> --type agent --role <role> --output json
agora agent update <agent-id> --archived true    # retire a subagent once its task is done
```

See `agora-creating-agents` for the full field contract (what's validated,
what the daemon actually reads, env/secret handling). A leader choosing to
spin up a subagent should: pick skills via `agent skills set` (workspace
skills bound explicitly — creation does not bind any), pick an MCP config via
`--mcp-config-*` if the task needs external tools, add the new agent to its
own squad as a member so it's covered by the same routing/roster, and archive
it when the task is done rather than leaving idle agents around.

**Model/difficulty selection — label the issue or set the agent directly.**
`applyIssueCostTier` (`server/internal/handler/daemon.go`) resolves a task's
model+thinking from the issue's tier labels at claim time:

- `tier:trivial` → haiku, no thinking
- `tier:light` → sonnet, no thinking
- `tier:heavy` → opus, high thinking (the one tier that RAISES capability)
- no tier → the agent's own configured model/thinking

Labels are the race-free, per-issue lever: a lead can tier an issue up or
down without mutating a shared agent's config. Beyond those labels, or for a
subagent the lead creates fresh, set the agent directly — both `agora agent
create` and `agora agent update` take `--model` AND `--thinking-level`
(e.g. `--model claude-opus-4-8 --thinking-level high` for the hardest work,
`--model claude-sonnet-5` for fast execution). Note the timing: model and
thinking are read at task CLAIM time, so a change applies to the subagent's
NEXT task, never mid-run — tier the issue/agent BEFORE delegating, not while
a task is running.

## Autopilot behavior

Autopilots can be assigned to squads. For `assignee_type = "squad"`:

- executable agent resolves from `squad.leader_id`;
- admission/readiness checks run against the leader;
- archived squads fail closed / skip dispatch;
- run attribution records squad id where applicable.

For `create_issue` autopilots, the created issue keeps `assignee_type = "squad"`
and `assignee_id = <squad-id>`, while the actual executing agent is the resolved
leader. For `run_only` autopilots, no issue is created; the task is created
directly for the resolved leader agent.

## Handling complaints or product gaps

When the user says squad behavior is wrong, confusing, or disappointing, do not
immediately assume code is broken and do not defend current behavior just because
it exists. Classify first:

- expected current behavior;
- configuration issue;
- product limitation;
- actual bug.

Explain the current source-backed behavior. If the behavior is technically
correct but product-wise bad, say so and propose a scoped product/code change.

Do not silently change squad routing, member fan-out, leader briefing, autopilot
behavior, or comment-trigger behavior without confirmation. These are product
contract changes with side effects.

## Side effects

These actions can trigger agent work or mutate durable state:

- creating a squad;
- updating squad fields;
- changing `leader_id`;
- adding/removing members;
- changing member roles;
- assigning an issue to a squad;
- moving a squad-assigned issue out of backlog;
- commenting on a squad-assigned issue;
- mentioning a squad;
- creating or triggering squad-assigned autopilots;
- recording squad activity with `agora squad activity`;
- deleting/archive squad.

Do not perform side-effecting actions as tests unless the user explicitly
authorizes them.

## Common wrong assumptions

- A squad is not an agent.
- Squad work routes to `leader_id`, not every member.
- Squad mention routes to the leader, not every member.
- Squad assignment routes to the leader, not every member.
- Squad autopilot resolves to the leader as executable agent.
- `instructions` are leader briefing content, not automatic member prompts.
- `description` is not proven runtime prompt content.
- `role` is roster context, not automatic scheduling.
- Backlog assignment does not immediately start work.
- QA auto-trigger prefers the QA squad leader only when the dev side is
  squad-orchestrated — a solo dev agent still gets the load-balanced roster
  pick, not the leader.
- A squad leader is not capability-restricted — its task-token can create,
  configure, and archive other agents via the same `agora agent` CLI a human
  uses. Nothing special has to be granted for "leader" behavior.
- `tier:trivial`/`tier:light` labels are the ONLY automatic model selection.
  Everything above that is a leader judgment call, not the platform choosing.

## References

For source paths, tests, edge cases, and exact routing details, see:

```text
references/squad-source-map.md
```
