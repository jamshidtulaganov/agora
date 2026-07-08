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

If the command shape is unclear, check help instead of guessing:

```bash
agora squad --help
agora squad member --help
agora issue update --help
agora issue comment add --help
```

Do not assign, comment, mention, update, delete, or record squad activity just
to test. These can mutate workspace state or trigger agent runs.

## Core model

A Agora squad is a workspace routing and coordination object.

A squad is not an agent. It does not run work by itself. Current behavior:
squad-routed work runs through the squad's `leader_id` agent.

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
- `description` — human-facing metadata/display text. Do not assume runtime
  prompt impact unless source proves a consumer.
- `instructions` — squad-level instructions added to the squad leader briefing.
  They are not directly injected into every squad member.
- `avatar_url` — optional squad avatar URL.
- `leader_id` — agent ID of the squad leader; the runtime target for
  squad-routed work.
- `creator_id` — creator of the squad.
- `archived_at` / `archived_by` — archive metadata. Archived squads are rejected
  by assignment/autopilot routing paths.
- `member_count` — list response count of squad members.
- `member_preview` — list response preview of squad members.

Use `instructions` for leader-facing coordination policy: squad responsibility,
delegation expectations, when to ask humans, and review/handoff rules. Do not
write it as if every member automatically receives it.

## Squad member fields

- `member_type` — `agent` or `member`.
- `member_id` — ID of the agent or workspace member.
- `role` — roster role label. Current behavior: non-empty `role` appears in the
  leader briefing roster. Do not assume it creates scheduling, permissions, or
  routing behavior.

## Creation and leader membership

Creating a squad requires `leader_id`. The leader must be a workspace agent.
Create/update does not reject an archived leader: the lookup only checks the
agent exists in the workspace. An archived leader fails closed later, at
routing/dispatch — assignment, autopilot admission, and the comment/mention
readiness gate all reject an archived leader before any task is enqueued.

On create, the backend attempts to add the leader as a squad member with role
`leader`. When updating `leader_id`, if the new leader is not already a member,
the backend adds the new leader as a squad member with role `leader`.

## Leader briefing

For squad leader tasks, Agora appends a squad leader briefing to the leader
agent instructions. The briefing includes:

- Squad Operating Protocol;
- Squad Roster;
- Squad Instructions, only when `instructions` is non-empty.

Roster entries include member name, member type, mention markdown, and non-empty
role. Archived agent members are skipped from the briefing roster.

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
When a parent's sub-issues came from an approved *design proposal* (each carries
a `design_plan_index` in its metadata and a "Design context" section in its
description), the platform promotes the waiting `backlog` siblings automatically
the moment their prerequisites finish. The child-done system comment on such a
parent says so explicitly. As leader, never flip a design sub-issue's status
yourself — you would double-promote or start a sub-issue whose dependencies are
not yet met. Just work the sub-issues that are already `todo`.

Assignment validation rejects a missing type/id pair, non-existent squad,
archived squad, archived leader, and private leader when the actor cannot access
it.

## Comment and mention behavior

If an issue is assigned to a squad, a new comment can wake the squad leader. This
is leader routing, not member fan-out.

Squad mention format:

```md
[@Squad Name](mention://squad/<squad-id>)
```

Current behavior: resolve the squad, read `leader_id`, enqueue a leader task,
and use the current comment as the trigger comment. It does not enqueue every
squad member.

## QA squad leader routing

The auto-QA trigger (`in_review`) and the QA-fail auto-reassignment both prefer
squad LEADERS over individual agents when the work is squad-orchestrated —
this is the "dev lead and QA lead are always in communication" product rule,
not a general squad behavior. Two separate mechanisms:

- **`in_review` → auto `run_qa`.** If the issue's assignee is a squad, or an
  agent who belongs to any squad, the trigger routes to the QA squad's LEADER
  specifically (matched by squad name containing "qa", case-insensitive) —
  not the least-busy pick from the whole QA roster. Solo-agent / non-squad
  assignments are unchanged: they still fan across the whole QA roster so
  many `in_review` issues run concurrently.
- **Manual QA actions → QA lead, never the dev.** A manually-fired QA-family
  slice action (`run_qa` / `gen_test_cases` / `run_test_cases`, e.g. the QA
  review page's "Re-run QA") with no explicit agent defaults to the QA squad
  LEADER — not the issue's dev assignee. QA validation is owned by QA, never by
  the developer whose work is under test. Falls back to the assignee/own-agent
  only when the workspace has no ready QA squad leader.
- **`in_progress` → shift-left QA prep.** The moment a task enters
  `in_progress`, a QA agent gets a background `gen_test_cases` task: author
  the cases AND compile their Playwright scripts against the project QA
  manifest while the dev is still implementing — no diff-reading, no
  execution. By `in_review` the suite already exists, so the gate only
  EXECUTES it. Idempotent: skipped when the issue already has test cases.
- **`qa:fail` label → auto-reassignment.** The issue is handed back to the
  FAILING dev agent's squad leader (not the failing agent itself, not a
  human), status resets to `todo`, and a comment carrying the QA verdict
  summary is posted with an `@leader` mention — that comment IS the QA↔dev
  communication; it lands in the issue's one shared timeline so both the
  dev-facing Issue Detail and the QA review page read the same story.

Both are opt-in behind env gates (`AGORA_AUTO_QA_ENABLED`,
`AGORA_QA_FAIL_AUTOROUTE_ENABLED`) and both degrade silently to today's
manual/load-balanced behavior when there's no squad on one side — e.g. a
solo dev agent with no squad keeps the old flow; a squad dev with no QA
squad in the workspace falls through to the generic roster pick.

When auto-QA routes to the QA LEAD (dev side orchestrated), the instruction
is framed as a DELEGATION directive: the QA lead is told to hand the actual
gate run to a QA member (executed on a faster model) and own the
qa:pass/qa:fail rollup, rather than run the mechanical gate itself. The lead
orchestrates; a member executes.

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
NOT already carry `qa:pass`, and it isn't already in `in_review`. The
redirect fires `maybeRunQAOnInReview` (routing to the QA lead), so the loop
is dev → in_review → QA → `qa:pass` → done. Once `qa:pass` is present, the
`→done` write passes through untouched, so the loop always converges. This
means a dev lead that "self-approves" its own subordinate's work and jumps
to `done` is silently routed through QA instead of bypassing it. The gate
applies on both the single-issue update and the board batch-update paths.
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
