# Agora Assistant improvement plan — critique

Date: 2026-09-17

Reviewed document: [Agora Assistant improvement plan](agora-assistant-improvement-plan.md), reconciled on 2026-09-17.

Scope: document and code review of the working tree at review time. Tests were not rerun for this critique. Implementation is changing concurrently; evidence below describes the inspected snapshot, not a permanent statement about the product.

## Assessment

The direction is strong, but the plan overstates some guarantees and leaves important dependencies out. Keep full UI parity as the objective; tighten the execution contracts and evidence before declaring completion.

The useful product sequence remains: daily work, planning and delegation, then reusable reports. The main improvements needed are precise completion claims, enforceable authorization, dependable recovery, and reproducible verification.

## 1. P0 — Confirmation is not yet equivalent to a user clicking Confirm

The roadmap's main rule requires explicit user confirmation for destructive actions. However, `assistantConfirmed` only checks whether the model supplied `confirm: true`. It does not verify a corresponding user confirmation for that target.

Evidence: [assistant_tools.go](../server/internal/handler/assistant_tools.go), `assistantConfirmed` and its call before tool dispatch.

**Required change:** bind confirmation to a persisted user action, exact operation, target, and arguments. A changed target or proposal must not inherit confirmation for the old one. This implements the intended UI parity without adding repeated approval prompts.

**Acceptance:** missing confirmation, model-invented confirmation, confirmation for another target, and stale confirmation cannot authorize the operation; a valid confirmation executes the intended operation.

## 2. P0 — Mutation receipt claims exceed the implemented guarantee

The status table calls the implementation “idempotent-replay receipts for mutating tools.” This conflates two guarantees:

- Repeating the same message request ID returns the existing run.
- Repeating a business operation cannot duplicate its effects.

The first exists. The second is incomplete: writes execute before their results are recorded. Interrupted operations become uncertain; a new request can repeat them. This is a documentation overclaim, not evidence that the tested request-retry path duplicated effects.

Evidence:

- [run_store.go](../server/internal/assistant/run_store.go): request replay lookup and acceptance.
- [service.go](../server/internal/assistant/service.go): pending operation insertion, tool execution, then receipt update.
- [Migration 197](../server/migrations/197_assistant_run.up.sql): uniqueness is scoped to the run and tool call.

**Required change:** describe the current implementation as “request deduplication and mutation attempt receipts; uncertain effects require review.” Make Phase C's exactly-once task creation depend explicitly on versioned proposal identity and domain idempotency. Reconcile external effects separately.

Recovery also needs operation-level results in the UI. A generic interrupted-run message does not tell the user which changes succeeded or require inspection.

**Acceptance:** interrupt execution after a committed effect but before receipt persistence; recovery must identify the uncertainty and must not blindly repeat the effect.

## 3. P0 — HTML containment needs a broader gate

The roadmap correctly identifies outbound data exposure, but “the user already saw the data” does not reduce the consequence of sending it to an external host. Sharing is not required for that exposure.

Evidence: [CodeBlockIframe](../packages/views/editor/code-block-iframe.tsx) renders supplied HTML with `srcDoc` and `sandbox="allow-scripts"`; the inspected implementation has no artifact-specific CSP wrapper.

**Required change:** make containment a gate before enabling HTML artifacts for users. Test outbound requests through scripts, images, styles, frames, and navigation, not only `fetch()`. A blocked fetch alone does not establish full containment: CSP distinguishes resource controls from navigation controls. See the [MDN CSP reference](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Content-Security-Policy).

Define the supported interactive HTML behavior and verify containment in the supported web and desktop environments. Keep the sandbox restriction; CSP complements it.

## 4. P1 — Scheduled refresh needs a defined execution scope

The [artifact spec](agora-assistant-artifacts-plan.md), Phase 3, proposes rerunning the assistant with the owner's permissions and potentially the original conversation prompt. A report refresh could inherit instructions intended for an earlier interactive action.

**Required change:** authorize a stored refresh definition with permitted sources, scope, and destination artifact. Allow reads plus updating that artifact, and recheck membership each run. Specify overlap prevention, event deduplication, retry limits, and spending limits.

This preserves full interactive capabilities while giving background work a defined purpose. Permission to refresh a report does not by itself specify unrelated issue updates or agent dispatches.

**Acceptance:** revoked access stops affected reads; duplicate events do not duplicate refreshes; overlapping runs do not overwrite newer results; refreshes cannot perform unrelated mutations.

## 5. P1 — Memory and resource budgets are missing dependencies

The service still supplies only 30 transcript rows to the model. The review found summary consumption but no assistant summary-writing path. Longer planning conversations can lose earlier constraints.

Evidence: [service.go](../server/internal/assistant/service.go), `HistoryLimit` and `history`; [prompt.go](../server/internal/assistant/prompt.go), summary insertion.

**Required change:** restore complete-turn context budgeting, working memory, and long-conversation tests before Phase C. Add usage accounting and user/instance concurrency limits before scheduled work. Per-run time limits do not bound aggregate usage.

**Acceptance:** a long conversation retains the active objective, corrections, accepted proposal version, and completed operations while preserving valid tool exchanges. Aggregate limits remain effective across multiple sessions and scheduled jobs.

## 6. P1 — Verification claims need reproducible evidence

“Backend suites green” and the stress results lack linked logs, a tested revision, provider configuration, and scenario results. The referenced `scratchpad/stress_assistant.py` was absent from this checkout at review time.

Calling two failures “non-assistant” helps assign ownership, but those failures still affect the user's workflow.

**Required change:** record both end-to-end success and assistant-specific correctness. Attach commands, revision or working-tree snapshot, environment, provider/model configuration, results, and known failures. Keep “implemented,” “verified,” and “deployed” distinct.

The existing [browser recovery test](../e2e/assistant-recovery.spec.ts) mocks assistant APIs. It usefully checks page/cache/draft behavior, but does not establish real server restart recovery or real-model task success. Those need separate evidence.

**Acceptance:** another engineer can reproduce the release checks and identify which guarantees each test layer actually covers.

## Planning corrections

### Define parity with a coverage matrix

Map each supported UI action to its tool, role checks, confirmation behavior, side effects, and tests. Tool count alone cannot establish full UI parity. Mark gaps as planned, implemented, or verified.

### Reconcile companion specifications

- The artifacts spec reserves migration 197 for refresh fields, but durable runs already use it. Allocate a new migration when implementation begins.
- The artifacts spec describes version history, while the current [update query](../server/pkg/db/queries/assistant_artifact.sql) overwrites content and increments a number. A version counter does not preserve prior content. Add revision storage or correct the history claim.
- Assign the activity-logging and model-label backlog items to delivery phases; they currently have no explicit phase.

### Re-estimate Phase A

The 2–4 day estimate is optimistic for parity integration, confirmation enforcement, HTML containment, reporting correctness, and verification. Split these into reviewable deliverables and estimate after the coverage matrix and acceptance cases are written.

## Recommended delivery order

1. Reconcile status claims and preserve reproducible test evidence.
2. Finish confirmation, uncertain-operation recovery, and HTML containment.
3. Deliver “My day” with accurate scope, totals, timezone, and context.
4. Add working memory, proposals, and delegation.
5. Add reproducible reports, then bounded scheduled refreshes.

The best next milestone is one dependable daily workflow with visible evidence of what happened. Keep parity progressing alongside it.

## Review conclusion

Retain the product direction and full UI parity goal. Revise the roadmap's completion claims, execution contracts, and release evidence before treating it as an implementation-ready commitment.

This critique does not establish a passing build or reproduce a production incident. It records specific gaps observed in the plan and implementation at review time.
