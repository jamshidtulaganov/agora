# Review stage v2 — "agent reviews, human approves"

The reviewer becomes a first-class, label-backed gate between QA and the
merge, and the merge itself becomes a HUMAN decision. QA proves the change
*works*; the review proves the change is *good code*; a human proves someone
*accountable* said ship it.

## Chain

```
qa:pass label lands (capture / CLI attach / human override)
        │
        ▼
maybeRunReviewOnQAPass                      [flag AGORA_AUTO_REVIEW_ENABLED]
  guards: review verdict absent · known PR exists · no dispatch in flight
  reviewer = dev squad leader ≠ author → other squad member → QA leader
        │  @mention comment, template slice_action_templates/run_review.md
        ▼
reviewer agent reads the PR diff (gh pr diff <n> / git diff merge-base)
  posts prose review + fenced ```review-result``` JSON block
        │
        ▼
CaptureReviewEvidence (service, label-FIRST — CaptureQAEvidence contract)
  attaches review:pass (#22c55e) / review:fail (#ef4444)
  replace-on-write: opposite label detached
  publishes issue_labels:changed with the FULL label set
  typed inbox notify on a NEW attach:
     fail → review_failed (action_required)
     pass + other gates green → merge_ready (action_required)
     pass otherwise → review_passed (info)
        │
        ▼
merge-readiness: "review" is REQUIRED for full-tier issues WITH a PR
  (trivial/light tiers and PR-less issues: gate omitted entirely)
maybeMergeOnQAPass: when the review gate applies, the merge chain waits for
  BOTH qa:pass AND review:pass (review:fail always holds); the human-facing
  READY note is marker-deduped
        │
        ▼
HUMAN clicks (POST /api/issues/{id}/review-decision, RequireHumanActor):
  approve          → gates verified (qa:pass present, review:fail absent;
                     merge:override bypasses) → merge:approved label →
                     system comment → member-authored @mention orders the
                     dev squad leader to `gh pr merge` (cautious wording on
                     risk-tier critical/guarded, but never refused — the
                     human's click is the consent)
  request_changes  → note required → status back to in_progress →
                     @mention the AUTHOR agent with the note + a pointer to
                     the findings; review:fail KEPT until a re-review
                     replaces it
        │
        ▼
fresh cycle (in_review re-entry): clearStaleQAGateLabels also detaches
  review:pass / review:fail / merge:approved
```

## Contracts

### `run_review` slice action

New kind in `server/internal/handler/slice_action.go`; template embedded from
`slice_action_templates/run_review.md`. The reviewer: locates the PR (metadata
`pr_number`, `gh pr list`, or merge-base diff without `gh`), reviews the diff
against the issue requirement on correctness / security / repo conventions
(target repo's CLAUDE.md) / scope creep / missing tests, NEVER edits code,
NEVER merges, NEVER re-runs QA. Manual fire:
`POST /api/issues/{id}/slice-actions {"kind":"run_review"}` — reviewer
resolution skips the issue's assignee (the author).

### ```review-result``` block

```json
{
  "verdict": "pass" | "fail",
  "summary": "one line",
  "commit_sha": "<PR head sha, 7-40 hex, else discarded>",
  "files_reviewed": 7,
  "findings": [
    {"file": "path", "line": 42 | null,
     "severity": "blocker" | "major" | "minor",
     "title": "short", "detail": "why + what to do"}
  ]
}
```

`verdict` is `fail` iff at least one finding is a `blocker`; major/minor alone
= pass with notes. The server capture is the idempotent authority; the agent
also sets the label via CLI as belt-and-braces. No new DB table — findings
live in the comment, resolved newest-first.

### `GET /api/issues/{id}/review-verdict`

```json
{
  "verdict": "pass" | "fail" | "none",
  "summary": "…", "commit_sha": "…", "files_reviewed": 7,
  "findings": [{"file","line","severity","title","detail"}],
  "comment_id": "<uuid>", "reviewed_at": "<RFC3339>",
  "reviewer_agent_id": "<uuid>"
}
```

`verdict:"none"` (with `findings: []`) when no captured verdict exists.

### `POST /api/issues/{id}/review-decision` (human-only)

Body: `{"action": "approve" | "request_changes", "note": "…"}`.

- `approve` → `200 {"action":"approve","merged_dispatch":true|false}`;
  409 `qa_gate_not_passed` / `qa_failed` / `review_failed` on gate
  violations (bypassed by `merge:override`); `merged_dispatch:false` when no
  dev squad leader resolves (the human merges by hand).
- `request_changes` → `200 {"action":"request_changes","status":"in_progress",
  "dispatched":true|false}`; 400 when the note is empty.
- 403 for machine actors (task tokens / cloud PATs).

### Merge readiness

`GET /api/issues/{id}/merge-readiness` gains a `{"name":"review", "status":
"pass"|"fail"|"pending"}` gate row for full-tier issues with a known PR
(`requiredGatesWithReview`). Status via `gateFromLabels("review")`.

## Labels

| Label | Color | Meaning |
|---|---|---|
| `review:pass` | `#22c55e` | reviewer found no blockers (replace-on-write with `review:fail`) |
| `review:fail` | `#ef4444` | at least one blocker finding |
| `merge:approved` | `#2563eb` | a human clicked Approve & merge (distinct from `merge:override`) |

All three are cleared by `clearStaleQAGateLabels` on a genuine in_review
re-entry.

## Flags

| Flag | Default | Effect |
|---|---|---|
| `AGORA_AUTO_REVIEW_ENABLED` | off | qa:pass → auto-dispatch `run_review` (reviewer ≠ author, PR required) |

The merge gate (`review` in required gates) and the endpoints are NOT
flag-gated — they activate the moment a review verdict exists; only the
auto-dispatch is opt-in. Existing flags unchanged: `AGORA_SPRINT_PR_MODE`,
`AGORA_SPRINT_AUTO_MERGE` (auto-merge now additionally waits for review:pass
when the gate applies).

## Inbox types

| Type | Severity | When |
|---|---|---|
| `review_failed` | action_required | review:fail landed |
| `merge_ready` | action_required | review:pass landed and qa:pass present (no qa:fail/ci:fail) — "awaiting your approval" |
| `review_passed` | info | review:pass landed but another gate is red/pending |

## Source

- `server/internal/handler/review_action.go` — auto-dispatch + reviewer resolution + gate applicability
- `server/internal/handler/review_decision.go` — human decision endpoint
- `server/internal/handler/review_verdict.go` — verdict read endpoint
- `server/internal/handler/slice_action_templates/run_review.md` — reviewer instruction
- `server/internal/service/review_evidence.go` — parse + label-first capture
- `server/internal/service/review_notify.go` — typed inbox notifications
- `server/internal/handler/merge_readiness.go` — review gate in required set
- Tests: `handler/review_test.go`, `service/review_evidence_test.go`, `service/review_notify_test.go`

## Frontend (phase 2, separate commit)

`packages/core`: `getReviewVerdict` / `reviewDecision` API methods with zod
schemas (parse-don't-cast), `ReviewVerdict`/`ReviewFinding` types, inbox types
`review_failed | review_passed | merge_ready`, `deriveReviewStage` signal
order upgrade. `packages/views`: review-lens v2 — verdict card, Approve &
merge / Request changes action bar, findings list, stale-sha hint, empty
state. i18n in all 4 locales.
