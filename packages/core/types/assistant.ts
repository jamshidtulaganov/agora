// Agora Assistant — system AI, cross-workspace. See docs/agora-assistant-plan.md.
//
// These types are USER-scoped: no workspace_id on session or message. Workspace
// scoping happens per tool call, inside the server's tool executor.

export interface AssistantSession {
  id: string;
  title: string;
  focus_workspace_id: string | null;
  created_at: string;
  updated_at: string;
  latest_run?: AssistantRun | null;
}

export interface AssistantRunContext {
  workspace_id: string | null;
  timezone?: string;
  project_id?: string | null;
  /** Teammate attached as person-context for this message (a user id). */
  member_id?: string | null;
  attachment_ids?: string[];
}

export type AssistantRunStatus = "queued" | "running" | "completed" | "failed" | "cancelled" | "interrupted";

export interface AssistantRun {
  id: string;
  session_id: string;
  message_id: string;
  /** Unknown future values are retained so callers can render a safe fallback. */
  status: AssistantRunStatus | (string & {});
  active_tool: string | null;
  error: string | null;
  created_at: string;
  updated_at: string;
  finished_at: string | null;
  version: number;
  context: AssistantRunContext;
}

export interface SendAssistantMessageRequest {
  content: string;
  request_id: string;
  context?: AssistantRunContext;
}

export interface AssistantToolCall {
  id: string;
  name: string;
  arguments: string;
}

export interface AssistantMessage {
  id: string;
  session_id: string;
  role: "user" | "assistant" | "tool";
  content: string;
  /** Present on assistant-role rows that requested tool calls this turn. */
  tool_calls?: AssistantToolCall[];
  /** Present on tool-role rows: which call this row answers. */
  tool_call_id?: string | null;
  /** Present on tool-role rows: the tool name, for rendering an action chip. */
  tool_name?: string | null;
  /** Present on tool-role rows: the raw structured result (may include links). */
  tool_result?: unknown;
  created_at: string;
}

export interface AssistantAvailability {
  enabled: boolean;
  model_label: string;
}

export interface CreateAssistantSessionRequest {
  title?: string;
  focus_workspace_id?: string;
}

export interface PatchAssistantSessionRequest {
  title?: string;
  /**
   * An explicit `null` CLEARS the focus (the session goes back to "all
   * workspaces"); omitting the field leaves the current focus alone. The two
   * are different requests, which is why this is `string | null` and not an
   * optional string — see the context chip in packages/views/assistant.
   */
  focus_workspace_id?: string | null;
}

export interface SendAssistantMessageResponse {
  message_id: string;
  run_id: string;
  created_at: string;
}

// --- Confirmation binding ----------------------------------------------
// A destructive tool call that lacks a bound human confirmation persists a
// pending operation and answers with a `needs_confirmation` tool_result; the
// transcript renders a ConfirmCard whose buttons call the two endpoints below.
// See docs/agora-assistant-final-plan.md ("Pinned wire contract").

/**
 * Flattened answer of `POST /api/assistant/operations/{id}/confirm` and
 * `.../reject`.
 *
 * The authoritative outcome is the receipt the server persists as a normal
 * `role:"tool"` message (delivered over WS and pulled by the transcript
 * invalidation), so every field here is advisory and may be blank — `reject`
 * is specified as a bare 204, and confirm answers with a nested
 * `{operation, message}` body the client flattens to this.
 */
export interface AssistantOperationDecision {
  /** Operation status after the decision, e.g. "confirmed" / "rejected". */
  status: string;
  operation_id: string;
  /** Receipt message the decision produced, when the server reports one. */
  message_id: string;
  /**
   * Per-item execution outcomes of a PLAN confirm (see AssistantPlanItem).
   * Absent for every single-operation confirm — including the ones an older
   * runtime answers — so its absence is the normal case, not drift.
   */
  items?: AssistantPlanItem[];
}

/**
 * One row of a plan operation (docs/assistant-domain-plan.md, "3a wire
 * contract"). The wire carries a SUBSET of these fields depending on the
 * stage: a parked plan sends `{index, tool, summary}`, the confirm answer and
 * the stored receipt send `{index, outcome, identifier?, error?}`. One type
 * covers both because the card renders them as one list — the proposal rows
 * with their outcome glyph attached.
 *
 * `outcome` stays a plain string, never a union: a value this build doesn't
 * know must render neutrally instead of failing the whole receipt.
 */
export interface AssistantPlanItem {
  /** 0-based position in the plan — what `skipped_items` refers to. */
  index: number;
  tool: string;
  summary: string;
  /** "ok" | "failed" | "skipped" | "not_run" once executed; "" before. */
  outcome: string;
  /** Human-readable id the item produced or touched, e.g. `MUL-123`. */
  identifier: string;
  error: string;
}

/** Persisted confirmation state; outcome is the execution result, not the click. */
export interface AssistantOperation {
  id: string;
  tool_name: string;
  summary: string;
  workspace_slug: string;
  target: { type: string; identifier: string; title: string } | null;
  status: string;
  outcome?: string | null;
  created_at?: string;
  expires_at?: string;
  /**
   * "plan" for a batch operation. ABSENT on every single-call operation,
   * which is precisely what makes absence mean "single op" — an older
   * runtime that never heard of plans keeps rendering the ConfirmCard.
   */
  kind?: string;
  /** Plan rows. Empty for a single operation, and empty when the server sent
   *  a malformed list — the card then falls back to single-op rendering. */
  items?: AssistantPlanItem[];
}

// --- Assistant artifacts -----------------------------------------------
// Standalone rich outputs the assistant produces (chart / table / markdown /
// html), rendered in the artifact pane. See
// docs/agora-assistant-artifacts-plan.md §3-§6. Owner-scoped like the
// session — no workspace_id (an artifact may aggregate cross-workspace data).

/** The four kinds the current contract defines. `kind` on the wire is kept
 *  as a plain string so a future server-side kind downgrades to the raw-
 *  content view instead of failing to parse (enum drift downgrades). */
export type AssistantArtifactKind = "chart" | "table" | "markdown" | "html";

/** List-endpoint row: everything except the (potentially large) content. */
export interface AssistantArtifactSummary {
  id: string;
  session_id: string;
  title: string;
  /** One of AssistantArtifactKind, or an unknown future kind. */
  kind: string;
  /** 1 on create, bumped by every update_artifact. */
  version: number;
  created_at: string;
  updated_at: string;
}

/** Detail endpoint: the summary plus the artifact body (spec JSON or text). */
export interface AssistantArtifact extends AssistantArtifactSummary {
  content: string;
}

// --- Artifact revisions -------------------------------------------------
// `update_artifact` bumps the artifact's version and appends an immutable
// revision row, so a dashboard the assistant has refreshed ten times is still
// inspectable at any point in between. The list read carries no bodies (up to
// 256 KB each); the detail read carries exactly one.

/** One entry in the pane's version picker. */
export interface AssistantArtifactRevisionSummary {
  id: string;
  artifact_id: string;
  /** 1-based; matches the artifact's `version` at the time it was written. */
  version: number;
  title: string;
  created_at: string;
}

/** A historical version in full — rendered READ-ONLY by the pane. */
export interface AssistantArtifactRevision extends AssistantArtifactRevisionSummary {
  content: string;
}
