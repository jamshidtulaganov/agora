/**
 * Escalations — an agent stopped and asked a human.
 *
 * Agora's agents structurally cannot ask a question mid-run (every Claude
 * invocation carries `--disallowedTools AskUserQuestion`, because a headless
 * run has no UI for a prompt to render in). An escalation is the out-of-band
 * state that replaces the old convention of "put the question in a comment":
 * a comment does not park the run, does not reach an inbox, and does not stop
 * the agent from guessing and carrying on.
 *
 * Exactly one escalation per issue may be `open` at a time — enforced by a
 * partial unique index server-side, so a second raise refines the same row
 * rather than stacking a second question onto the same person.
 */

/**
 * Why the agent stopped. Server-driven, so treat it as an open set: render a
 * generic fallback for a value this client has not seen (see the enum-drift
 * rule in CLAUDE.md).
 */
export type EscalationKind =
  /** An ambiguity the agent cannot resolve by reading. */
  | "question"
  /** Cannot proceed at all without something only a human can supply. */
  | "blocked"
  /** A spend / turn / wall-clock budget ran out. Raised by the server, never by the agent. */
  | "budget"
  /** An access decision the agent must not take itself. */
  | "permission"
  /** The change is riskier than the agent was cast to decide. */
  | "risk";

export type EscalationStatus = "open" | "answered" | "cancelled";

export interface Escalation {
  id: string;
  workspace_id: string;
  issue_id: string;
  /** The parked run. "" when the escalation was raised outside a task. */
  task_id: string;
  agent_id: string;
  kind: EscalationKind;
  /** What the agent needs, in one sentence. */
  prompt: string;
  /** What it already tried. May be "". */
  detail: string;
  /** Concrete alternatives the human can pick. Empty ⇒ free-text answer. */
  options: string[];
  /** Risk tier snapshotted when the escalation was raised; "" when untiered. */
  risk_tier: string;
  status: EscalationStatus;
  /** The human's answer. "" while open. */
  answer: string;
  answered_by: string;
  answered_at: string;
  /** The run the answer produced. "" when nothing was resumed. */
  resumed_task_id: string;
  raised_at: string;
}

export interface EscalationListResponse {
  escalations: Escalation[];
}
