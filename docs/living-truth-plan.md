# Living truth — the tracker that stays right on its own

Status: G1 from docs/market-research-synthesis.md · designed 2026-09-19 ·
building now

## Thesis

The market's #1 unclaimed problem (verbatim, top-voted, 2026): "Everyone's
solved getting context into the issue. Nobody's solved the issue quietly
becoming wrong afterward — PR merges and status doesn't move, a date slips,
something gets descoped verbally. Agents make it worse: they generate state
changes faster than anyone updates the tracker."

Agora already half-solves this and tells nobody: the GitHub webhook advances
an issue to done when its last close-intent PR merges (github.go:850's
careful three-rule gate), and a failed task resets a stuck issue to todo
unless an in-flight PR argues otherwise. What's missing:

1. **Provenance.** advanceIssueToDone writes no trail — the status jumps and
   the team can't see why. Silent auto-moves are how trust dies ("who moved
   my issue"); attributed auto-moves are how it compounds.
2. **The start of the loop.** A close-intent PR opening does not move a
   todo issue to in_progress — the most obvious true signal is dropped.
3. **Staleness.** No surface anywhere says "this issue looks wrong". That is
   the whole Tier 2.

## Design

### Tier 1 — deterministic derivation (auto-apply, always attributed)

Facts, not inference — safe to write, and every write says why:

- **PR opened/linked with close intent** → issue in backlog/todo moves to
  in_progress. Forward-only: never regress a further-along status.
- **Existing merged→done path keeps its rules** (three-rule gate stays
  byte-identical).
- **Every derived move now writes provenance**: a system activity/comment on
  the issue — "Status: in_progress → done. PR #42 merged (close intent)." —
  through whatever the existing system-comment path is. WS events unchanged.

Never auto-derived (deliberate): blocked (human judgment), cancelled (human
decision), anything backward except the existing failed-task reset.

### Tier 2 — staleness detection (compute, surface, never write)

A stale signal is inference, so it NEVER changes status — it renders. State
is computed on read from what already exists (no stored staleness, nothing
new to go stale itself):

| reason | rule (defaults) |
| --- | --- |
| `idle` | in_progress · no active task · no open linked PR · no activity ≥ 5d |
| `review_done` | in_review · has linked PRs · all merged/closed ≥ 2d |
| `reopened_work` | done · at least one linked PR open/draft |
| `blocked_quiet` | blocked · no activity ≥ 7d |

Thresholds: instance-config keys `AGORA_STALE_IDLE_DAYS`,
`AGORA_STALE_REVIEW_DONE_DAYS`, `AGORA_STALE_BLOCKED_DAYS` (KindInt,
Category "Truth", the listed defaults). "Activity" = the freshest of issue
updated_at, latest comment, latest task update for the issue.

Delivery is a **separate query, not a join into the hot list path**: one
sqlc query per workspace (optionally filtered by project) returning
`{issue_id, reason, since}` rows; `GET /api/issues/staleness` (workspace-
scoped, X-Workspace-ID, optional `project_id`); frontend fetches it beside
the list and merges client-side. Board/list perf untouched.

### Surfaces

- **Issue list/board rows + issue detail**: a quiet staleness indicator
  (muted clock glyph + tooltip naming the reason and the age; detail page
  adds one sentence). No color screaming; this is a nudge, not an alarm.
- **Assistant**: new read tool `list_stale_issues` (workspace, optional
  project) returning the rows joined with identifier/title/status; the
  SPRINT REPORT recipe's risks section is now told to call it and lead the
  risks with staleness; MY DAY mentions the user's own stale issues last.
- **WS**: none needed — staleness recomputes on the queries' normal
  refetch/invalidation cadence.

### Non-goals (this phase)

- No Slack/chat-signal inference ("date slipped in a message" is Tier 3,
  needs the Slack integration first).
- No auto-writes from staleness, no nag notifications, no digest emails.
- No per-project threshold overrides yet (instance config only).
- No throughput/velocity metrics of any kind (see research anti-goals).

## Build

Two agents, fixed contract, no file overlap: backend (Tier 1 provenance +
PR-opened start + staleness query/endpoint/tool/prompt/config + tests),
frontend (staleness query/schema + list/detail indicators + locales ×4 +
tests). Contract: response `{stale:[{issue_id, reason, since}]}` with
reason ∈ the four strings above; unknown reasons must degrade to a generic
"stale" rendering (enum-drift rule).
