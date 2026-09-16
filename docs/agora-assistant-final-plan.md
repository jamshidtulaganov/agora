# Agora Assistant — final proposed implementation plan

Date: 2026-09-17

Status: **ADOPTED 2026-09-17** (owner + Fable session) with three amendments, in implementation. Amendments: (1) confirmation is TIERED — model-boolean confirm suffices for reversible ops and single-user local dev; persisted-operation binding (below) is the gate before deletes/invites/role-changes reach multi-user rollout; (2) the HTML-artifact srcdoc CSP fix ships immediately, not phase-gated; (3) Phase 0 is compressed to commit-split + parity matrix.

### Pinned wire contract — confirmation binding & receipts (both sides build against this)

- A destructive tool call lacking a bound confirmation persists a pending operation and returns tool_result `{"status":"needs_confirmation","operation":{"id","tool_name","summary","workspace_slug","target":{"type","identifier","title"}}}`. The transcript renders a ConfirmCard (Confirm/Cancel buttons — the out-of-band human click).
- `POST /api/assistant/operations/{id}/confirm` → executes the bound operation server-side (re-checking permissions + target version), persists a `role:"tool"` receipt message, emits the normal `assistant:message` event. `POST /api/assistant/operations/{id}/reject` → 204 + a "cancelled" tool message. Confirm on a changed/expired operation → 409.
- Successful mutation tool_results gain `{"receipt":{"action","target","links":[],"effects":[]}}`; uncertain outcomes return `{"status":"uncertain","inspect":"..."}` and are never blindly replayed.

Status history: proposed revision, ready to break into implementation tasks. This document incorporates the [roadmap](agora-assistant-improvement-plan.md) and [critique](agora-assistant-plan-critique.md). It preserves the owner's full UI parity objective. Existing roadmap and detailed specifications remain unchanged until this revision is adopted.

## 1. Product goal

Make Agora Assistant a dependable way to understand work, change it, delegate it, and verify the result.

The assistant should answer three questions clearly:

1. What needs my attention?
2. What did you change?
3. What happens next?

The assistant can perform every supported action a user can perform manually in Agora, subject to the same role and resource permissions. Destructive actions require the equivalent of the UI's confirmation. Raw secret values are entered through their dedicated settings controls rather than persisted in chat.

Full parity is the destination. Deliver useful workflows incrementally, with a coverage matrix showing which actions are implemented and verified.

## 2. Current baseline and limits

These statements describe code inspected on 2026-09-17. Recheck the working tree before implementation because several changes are in progress.

| Area | Baseline | Remaining work |
| --- | --- | --- |
| Assistant service | Go model/tool loop, persisted conversations, provider adapters, shared web/desktop UI | Measure task success, latency, usage, and failure categories |
| Durable runs | Database leases, message request deduplication, cancellation, mutation attempt receipts | Domain idempotency, uncertain-effect reconciliation, operation-level recovery UI |
| Message context | Workspace/timezone captured for sends and retries | Entity context, explicit target resolution, consistent scope display |
| Client recovery | React Query run state, invalidation, persisted drafts and request identities | Complete integration and browser verification |
| Tool parity | Broad catalog and real-handler reuse; expansion in progress | Action-by-action coverage and permission/side-effect verification |
| Confirmation | Executor checks a model-supplied `confirm` boolean | Bind execution to an actual user confirmation for the exact operation |
| Artifacts | Stored artifacts and chart/table/markdown/HTML viewers | HTML containment, provenance, immutable revisions, authorized refresh |
| Conversation memory | Recent 30 transcript rows; summary consumption exists | Complete-turn budgeting and a verified summary/working-memory writer |
| Verification | Prior focused checks and stress results exist | Reproducible results for a stable working-tree snapshot and the full release gate |

Request deduplication does not guarantee exactly-once business effects. An interrupted mutation can have an uncertain outcome. Do not automatically replay it without reconciliation.

## 3. Architecture and contracts

### Keep the existing architecture

- Retain the Go assistant coordinator, typed tools, and existing agent orchestration.
- Reuse existing handler validation and role checks; extract shared domain commands where transaction boundaries, attribution, or idempotency require them.
- Keep server state in React Query and client drafts/preferences in Zustand.
- Use WebSocket events to invalidate queries.
- Preserve shared web/desktop views, NavigationAdapter routing, defensive API schemas, and four-language support.

### Resolve context explicitly

Capture workspace, selected entity, and timezone when a message is sent. Preserve that snapshot for retries.

An explicit user target takes precedence over page focus after resolution and permission checks. Ambiguous names require clarification. Display the resolved scope before a consequential action, and include it in operation receipts.

### Bind confirmation to the intended action

Persist a pending operation or proposal containing actor, target IDs, arguments, version, and described side effects. Record the user's confirmation against that object. Execution validates the binding and current permissions.

Changed targets or material argument changes invalidate old confirmation. Already-authorized ordinary actions should execute without unnecessary confirmation loops. Model-generated `confirm: true` is not evidence of a human confirmation.

### Make outcomes recoverable

Persist operation intent before execution and expose operation outcomes to the user:

- Succeeded: verified result and affected entities.
- Failed without effects: known validation or permission refusal.
- Uncertain: the operation may have taken effect; inspection is required.

Use stable domain operation identities for supported mutations. For Agora-owned database changes, make the change and its durable receipt atomic where feasible. External effects require their own idempotency or reconciliation strategy. Cancellation stops future work; it does not promise rollback of completed changes.

### Return honest data

List and aggregate results must describe scope, observation time, date range where relevant, pagination, truncation, and partial failures. Exact totals come from matching aggregate queries; unavailable totals remain unknown.

Cross-workspace answers identify checked and failed workspaces. All date windows use the captured timezone. Model text and artifact numbers must derive from returned data.

## 4. Delivery sequence

Effort ranges are provisional engineering days for one engineer familiar with Agora, excluding review queues. Re-estimate after Phase 0. Do not sum them into a delivery commitment before parity scope and external-effect requirements are known.

### Phase 0 — Establish a reproducible baseline

Indicative effort: 1–3 days.

- Reconcile active work and detailed specs without overwriting concurrent changes.
- Build the parity matrix: UI action → tool → role checks → confirmation → downstream effects → tests → status.
- Record a revision or working-tree snapshot, commands, environment, provider configuration, logs, and outstanding failures.
- Separate implemented, verified locally, release-ready, and deployed status.
- Select representative pilot workflows and record initial usefulness and completion measures.

**Gate:** every completion claim has evidence; remaining failures have an owner and next action. Artifact migration numbers and revision-history claims match reality.

### Phase 1 — Finish dependable execution

Indicative effort: 5–10 days, revised after the baseline.

- Implement confirmation binding and tests for missing, stale, mismatched, and model-invented confirmation.
- Expose operation receipts and uncertain outcomes through API and shared UI.
- Define idempotency/reconciliation for initial supported mutations, including downstream effects.
- Finish context display and entity resolution; preserve retry context and draft identity.
- Complete restart, reconnect, cancellation, lease-loss, and duplicate-send verification.
- Add a writes disable switch separate from full assistant disable.
- Complete HTML containment before enabling HTML artifacts for users. Retain the iframe sandbox and verify outbound resource and navigation behavior across supported browsers/desktop.

**Gate:** failures and interruptions produce accurate visible outcomes; tested retries do not duplicate effects; confirmation cannot authorize another target; HTML containment tests pass. Uncertain effects remain blocked from blind replay.

### Phase 2 — Deliver a trustworthy “My day” workflow

Indicative effort: 5–8 days.

- Fix list coverage, archive filtering, exact aggregates, and partial-workspace reporting.
- Use explicit timezone-correct windows for activity and usage.
- Repair missing activity records needed by the briefing.
- Rank work using due dates, priority, dependencies, review requests, and inactivity; distinguish inferred urgency from stored facts.
- Provide linked evidence and a concrete next action for each recommendation.
- Make permission errors clear and model identity match the configured UI label.

**Gate:** realistic fixtures produce correct scope, totals, date windows, and source links. Pilot users can identify and act on a useful next step.

### Phase 3 — Add memory, proposals, and delegation

Indicative effort: 8–14 days.

- Implement complete-turn context budgeting and working memory for objectives, corrections, references, decisions, and completed operations.
- Re-read mutable facts before acting; retain the previous valid summary if compaction fails.
- Add editable, versioned task proposals with partial selection and dependency handling.
- Bind acceptance to proposal version and create selected Agora-owned tasks using stable operation identities.
- Connect handoffs to existing orchestration and expose queued/running/blocked/review/verified states with evidence.
- Track usage and enforce user/instance concurrency limits.

**Gate:** long conversations retain important constraints; accepted proposals create the intended tasks without duplicate creation in retry tests; external uncertainties remain explicit; offline agents are never reported as running.

### Phase 4 — Make reports reproducible and refreshable

Indicative effort: 6–10 days.

- Store source references, scope, generation time, query parameters, and producing run for artifacts.
- Add immutable revision storage and concurrency checks; incrementing a version number alone is insufficient.
- Make initial report refresh user-triggered and reproducible.
- Add scheduled/event-driven refresh using a stored definition of permitted sources and one destination artifact.
- Restrict refresh execution to authorized reads and that artifact update; recheck access at every execution.
- Add overlap prevention, event deduplication, retry/backoff limits, spending limits, pause, and deletion behavior.

**Gate:** report values reproduce from recorded inputs; old versions remain inspectable; revoked access stops affected refreshes; duplicated events and overlapping jobs cannot corrupt results or trigger unrelated actions.

### Phase 5 — Add optional proactive assistance

Indicative effort: 3–5 days.

- Use existing automation infrastructure for explicitly configured digests and blocker notifications.
- Support timezone, quiet hours, frequency, deduplication, and easy disable.
- Keep notification subscriptions scoped to the authorized delivery. Separate action automation needs its own explicit scope.

**Gate:** notifications match the subscription and recipient; subscriptions do not authorize unrelated mutations; usefulness and dismissal rates justify continued delivery.

### Ongoing parity and performance work

Expand the parity matrix throughout the phases. Require the same execution and verification contracts for every new action.

Add transcript pagination, bounded concurrent independent reads, relevant tool selection, and streaming according to measured bottlenecks. Preserve write ordering and authoritative persisted state. These improvements must not delay the first useful daily workflow without evidence that they are required.

## 5. Verification and release gates

Maintain separate evidence for each layer:

| Layer | What it establishes |
| --- | --- |
| Unit tests | Schemas, context resolution, confirmation binding, state transitions, result formatting |
| Database/handler integration | Permissions, transactional effects, idempotency, cancellation, leases, and receipts |
| Browser tests with mocked APIs | Drafts, cache recovery, status rendering, navigation, and UI behavior |
| Full integration fault tests | Actual restart, disconnection, duplicate requests, and external-effect uncertainty |
| Repeated real-model scenarios | Tool selection, ambiguity handling, multilingual behavior, prompt-injection resistance, and task completion |
| Pilot observation | Usefulness, effort saved, corrections, abandonment, and repeat usage |

Start with 40–60 representative scenarios, including read-only requests, ambiguous entities, revoked membership, long conversations, timezone boundaries, partial data, duplicate effects, interrupted tools, and agent failures.

Proposed release targets, calibrated after baseline:

- Zero permission violations, unconfirmed destructive operations, or duplicate effects in the release suite; any occurrence blocks release.
- At least 95% simple supported task success across repeated real-model trials, with sample size and configuration reported.
- Exact agreement between report numbers and fixture queries.
- Every tested interruption reaches an accurate terminal or recoverable state.
- Initial p95 acknowledgment target below one second; record completion latency and usage separately.
- Required repository checks, including `make check`, pass for the recorded snapshot, with browser coverage identified explicitly.

Passing a finite suite is evidence for the tested scenarios, not proof of universal correctness. Report end-to-end workflow failures even when their root cause is outside the assistant.

## 6. Rollout and ownership

Release to an internal cohort, then a small enabled cohort, then expand based on measured gates. Record migrations and rollback/disable procedures for each release. This plan itself does not authorize production deployment or external message sending.

For parallel implementation, assign distinct ownership:

- Backend: execution contracts, persistence, permission checks, and tool/domain behavior.
- Core: API schemas, query state, retries, context, and client persistence.
- Views: scope display, confirmations, receipts, recovery states, and localized workflows.
- Integrator: shared contract agreement, migration allocation, evidence, and full verification.

Agree on API shapes before parallel edits. Run database suites sequentially when they share destructive fixture setup or cleanup.

## 7. Immediate next milestone

Complete Phases 0–1, then deliver one “My day” flow from Phase 2. The reviewable result should let a user see the target context, request a change, inspect its result, and recover from interruption without guessing what happened.

This is the foundation for full UI parity, reliable delegation, and useful recurring reports.
