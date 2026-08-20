---
name: agora-autopilots
description: "Use when creating, updating, inspecting, triggering, or debugging Agora autopilots. Covers the full chain: schedule/webhook/manual trigger, create_issue vs run_only execution, agent/squad leader admission, runs, created issues/tasks, webhook URL rotation, and side-effect boundaries."
user-invocable: false
allowed-tools: Bash(agora *)
---

# Agora Autopilots

## Quick start

Autopilots are durable automations. Read before mutating:

```bash
agora autopilot list --output json
agora autopilot get <autopilot-id> --output json
agora autopilot runs <autopilot-id> --output json
```

Do not run `trigger`, `delete`, `trigger-delete`, or `trigger-rotate-url` to test. Those are real side effects.

## Core model

An autopilot is not an agent. It is a rule that dispatches work to an agent, or to a squad's leader agent.

The chain is: trigger fires (`schedule`, `webhook`, or `manual`) -> `autopilot_run` row -> `execution_mode` decides output -> assignee readiness check -> issue/task execution -> run status sync.

Execution modes:

- `create_issue` creates a Agora issue, making the run visible as issue state.
- `run_only` creates an agent task directly. No issue is created; any durable
  report location has to come from other task context or instructions.

`issue-title-template` only supports `{{date}}`. Do not invent `{{trigger_id}}`, `{{branch}}`, or other variables.

### Autopilots vs automations — pick the right one

They are different features and answer different questions.

- An **autopilot** starts work on a SCHEDULE or an inbound WEBHOOK. It has no
  originating task; it creates one (or runs an agent directly). Use it for "every
  weekday morning", "when CI posts a deploy hook".
- An **automation** reacts to something that happened to an EXISTING task inside
  Agora: a status change, a label, an assignment, a comment, a tracker column move.
  It is stored as `WHEN trigger IF conditions THEN steps` (table `automation`), it is
  editable in the Automations UI, and every evaluation — applied OR skipped, with the
  reason — lands in `automation_run`. Steps can set a status, assign, add/remove a
  label, post a comment, dispatch a slice action (`run_review`, `run_qa`, …), or send
  a Telegram notice. There is deliberately no "call any URL" step.

So: "when the tracker moves a task to Code Review, review it and tell the room" is an
automation, not an autopilot. If a human asks you to build that, point them at the
Automations page (a recipe already installs it) rather than writing a new autopilot.

Automations guard themselves against feeding each other: every write they perform is
attributed to the actor type `automation` (which the engine ignores on the way back
in), and each rule applies to the same task at most once per cooldown and a bounded
number of times per hour. A rule that "did nothing" almost always has a `skipped` row
explaining which condition failed — read that before changing the rule.

For a Telegram step with an explicit group id, delivery prefers the issue's speaker
agent bot when it is authorized for that group, then any active workspace bot whose
allowed-groups list contains the id, and only then the platform bot. A `404 Not Found`
from Telegram on the platform path means that platform bot token is invalid; it does
not mean the automation condition failed.

Message templates support `{{issue}}`, `{{title}}`, `{{status}}`, `{{automation}}`,
`{{assignee}}`, `{{actor}}`, `{{source_url}}`, and `{{source_assignee}}`. Use
`{{assignee}}` for the current Agora task owner, `{{actor}}` for the member or agent
whose event triggered the flow, `{{source_url}}` for the canonical upstream task
link (`external_issue_url`, falling back to the Bitrix importer's `bitrix_task_url`),
and `{{source_assignee}}` for the upstream responsible person (`external_assignee`,
falling back to `bitrix_responsible_name` and then the Agora assignee).

Rules may condition on `source_assignee_email` and `source_creator_email` to route
notifications by upstream ownership. Provider-neutral metadata keys are preferred;
Bitrix mirrors populate them from `bitrix_responsible_email` and
`bitrix_created_by_email`.

Bitrix import assignment is human-first: a responsible person who resolves to a
workspace member remains the Agora assignee, including on a squad-bound project;
the review/project squad is only a fallback. For provider spelling differences or
legacy duplicate accounts, set `workspace.settings.bitrix_identity_aliases` to a
map of Bitrix email → canonical Agora email. Aliases are case-insensitive and run
before external-id and exact-email matching.

A human can retry a failed automation run with
`POST /api/automations/{automation_id}/runs/{run_id}/rerun`. The retry creates a new
audit row linked by `detail.retry_of` and executes only failed steps; successful
steps from the original run are not duplicated. The automation must still be enabled
and unchanged since the selected run.

## CLI

```bash
agora autopilot list --output json
agora autopilot get <autopilot-id> --output json
agora autopilot create --title "<title>" --description "<task prompt>" --agent <agent-name-or-id> --mode create_issue|run_only --output json
agora autopilot update <autopilot-id> --status active|paused --output json
agora autopilot runs <autopilot-id> --output json
agora autopilot trigger-add <autopilot-id> --kind schedule --cron "0 9 * * *" --timezone Asia/Shanghai --output json
agora autopilot trigger-add <autopilot-id> --kind webhook --label "ci" --output json
agora autopilot trigger <autopilot-id> --output json
agora autopilot trigger-rotate-url <autopilot-id> <trigger-id> --yes --output json
```

Use `trigger` only when the user explicitly asks for a manual run. Use `trigger-rotate-url` only when rotating a webhook URL; the old URL stops being valid.

Webhook trigger output can include a URL/token. Do not paste webhook tokens or signing material into comments, logs, docs, or PRs. Redact secrets.

## Debugging

For "why didn't it run":

1. `agora autopilot get <id> --output json` — status, mode, assignee, triggers.
2. `agora autopilot runs <id> --output json` — run status and failure reason.
3. If assigned to a squad, inspect the squad: `agora squad get <squad-id> --output json`; execution goes to the leader.
4. Inspect the target agent/runtime: `agora agent get <agent-id> --output json` and `agora runtime list --output json`.
5. For `create_issue`, inspect the created issue if the run records one.

## Side effects

These mutate durable state or start work: `create`, `update`, `delete`, trigger add/update/delete/rotate, `trigger`, and webhook calls to `/api/webhooks/autopilots/{token}`.

More source-backed details: `references/autopilots-source-map.md`.
