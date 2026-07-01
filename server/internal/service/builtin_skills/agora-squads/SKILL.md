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

**Model/difficulty selection is currently a leader judgment call, not full
automation.** `applyIssueCostTier` (`server/internal/handler/daemon.go`)
already downgrades the model automatically for two label tiers —
`tier:trivial` → haiku, `tier:light` → sonnet — and leaves everything else at
the agent's configured default. That covers only the cheap end. For anything
above `tier:light`, or for a subagent the leader is creating fresh, the
leader must choose `--model` itself based on the task's actual difficulty
(e.g. a mechanical rename vs. a cross-service architecture change) — don't
assume the platform picks the right tier beyond those two labels.

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
