/**
 * The decision queue — everything in the workspace that is waiting on a
 * human, in one ranked list (docs/orchestration-upgrade-plan.md §A2).
 *
 * There is exactly one scarce resource in Agora: a person's attention. An
 * agent's question, a change ready to merge, a QA verdict nobody has judged
 * and a failed review are four item KINDS of one object, not four inboxes —
 * split apart they reproduce the failure the research names ("agents DDoSing
 * our attention") instead of fixing it.
 *
 * Ranked server-side by `score` and computed on read (nothing is stored, so
 * the ranking can never itself go stale). The client preserves the server's
 * order — it does not re-sort — and only ever degrades what it cannot read.
 */

/**
 * What kind of decision is waiting. Server-driven, so treat it as an OPEN
 * set: a kind this build has never heard of must still render as a row
 * pointing at its issue, never be dropped and never blank the list (the
 * enum-drift rule in CLAUDE.md).
 */
export type DecisionQueueKind =
  /** An agent stopped and asked a person (§B1). */
  | "escalation"
  /** Gates are in; waiting on a person's merge approval. */
  | "merge_ready"
  /** A QA verdict needs a human call. */
  | "qa_failed"
  /** A review came back with problems a person must decide about. */
  | "review_failed";

/** The kinds this build has copy and a glyph for. */
export const KNOWN_DECISION_QUEUE_KINDS: readonly DecisionQueueKind[] = [
  "escalation",
  "merge_ready",
  "qa_failed",
  "review_failed",
];

/**
 * Risk tier snapshot used for ranking and for the row's chip.
 *
 * Deliberately a plain `string` on the wire: the resolver speaks
 * critical/guarded/safe/unclassified today and an installed desktop build
 * outlives any single server's vocabulary. `""` and `"unclassified"` both
 * mean "no opinion" — neither may render as "safe".
 */
export type DecisionQueueRiskTier = string;

/** The open escalation inlined on an `escalation` row, when there is one. */
export interface DecisionQueueEscalation {
  id: string;
  kind: string;
  /** What the agent needs, in one sentence. */
  prompt: string;
  /** What it already tried. May be "". */
  detail: string;
  /** Concrete alternatives. Empty ⇒ free-text answer. */
  options: string[];
}

export interface DecisionQueueItem {
  kind: DecisionQueueKind | string;
  issue_id: string;
  identifier: string;
  title: string;
  status: string;
  project_id: string;
  risk_tier: DecisionQueueRiskTier;
  /** How the tier was decided (glob match, self-report, …). Free-form. */
  risk_tier_source: string;
  /** When it started waiting (RFC3339). */
  since: string;
  /** Precomputed age, in hours. A cached response freezes it; `since` does not. */
  age_hours: number;
  /** The server's own one-line ask. Prefer this over any local copy. */
  needed: string;
  /** Stable identifier for `needed`, so a client can localize it. */
  needed_code: string;
  /** The rank. Rows arrive sorted by this, descending. Never re-sorted here. */
  score: number;
  /** Living-truth staleness rule that contributed, when one did. */
  stale_reason: string;
  open_pr_count: number;
  labels: string[];
  escalation?: DecisionQueueEscalation;
}

/**
 * EXACT counts, never rates. Throughput, velocity and per-person comparison
 * are banned from this surface (§D2) — the queue measures the queue.
 */
export interface DecisionQueueCounts {
  escalation: number;
  merge_ready: number;
  qa_failed: number;
  review_failed: number;
}

export interface DecisionQueueResponse {
  items: DecisionQueueItem[];
  /** How many decisions are waiting, including any the page did not list. */
  total: number;
  counts: DecisionQueueCounts;
}
