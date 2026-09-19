import type { Label } from "./label";

export type IssueStatus =
  | "backlog"
  | "todo"
  | "in_progress"
  | "in_review"
  | "done"
  | "blocked"
  | "cancelled";

export type IssuePriority = "urgent" | "high" | "medium" | "low" | "none";

export type IssueAssigneeType = "member" | "agent" | "squad";

export interface IssueReaction {
  id: string;
  issue_id: string;
  actor_type: string;
  actor_id: string;
  emoji: string;
  created_at: string;
}

/**
 * Per-issue metadata is a flat KV map agents use to record pipeline state
 * (PR number, pipeline_status, waiting_on, ...). Values are primitives only —
 * string / number / bool — enforced by both the API and the DB. Always
 * present in responses (empty object when unset) so reads don't need a
 * nil guard on the parent field.
 */
export type IssueMetadataValue = string | number | boolean;
export type IssueMetadata = Record<string, IssueMetadataValue>;

export interface Issue {
  id: string;
  workspace_id: string;
  number: number;
  identifier: string;
  title: string;
  description: string | null;
  status: IssueStatus;
  priority: IssuePriority;
  assignee_type: IssueAssigneeType | null;
  assignee_id: string | null;
  creator_type: IssueAssigneeType;
  creator_id: string;
  parent_issue_id: string | null;
  project_id: string | null;
  // Id of the sprint this issue belongs to, or null/absent when it belongs to
  // none. Bulk-attached by the list/detail endpoints alongside `labels` (PK
  // issue_to_sprint → one sprint per issue). Optional because write/broadcast
  // paths omit it; readers should treat absent as "unknown, leave as-is".
  sprint_id?: string | null;
  // Agent that OWNS this issue's pipeline — the orchestrator (squad lead, or
  // the solo agent itself). Every agent-run task has one (mandatory attach).
  // DETAIL-ONLY: only the single-issue GET attaches it; list/broadcast paths
  // omit it (resolving per-row would N+1), so treat absent as "unknown".
  orchestrator_agent_id?: string | null;
  position: number;
  // Calendar days as date-only "YYYY-MM-DD" (no time, no timezone). Use the
  // helpers in @agora/core/issues/date to format/compare — never `new Date()`
  // + local formatting, which shifts the day by the viewer's offset.
  start_date: string | null;
  due_date: string | null;
  metadata: IssueMetadata;
  reactions?: IssueReaction[];
  labels?: Label[];
  created_at: string;
  updated_at: string;
}

/**
 * Staleness — Tier 2 of docs/living-truth-plan.md. A stale signal is
 * INFERENCE, never a write: it is computed on read from issue / comment /
 * task timestamps and only ever renders. Nothing is stored, so nothing can
 * itself go stale.
 */
export const KNOWN_STALE_REASONS = [
  // in_progress, no active task, no open linked PR, quiet for N days.
  "idle",
  // in_review, has linked PRs, all merged/closed for N days.
  "review_done",
  // done, but at least one linked PR is open/draft again.
  "reopened_work",
  // blocked and quiet for N days.
  "blocked_quiet",
] as const;

export type KnownStaleReason = (typeof KNOWN_STALE_REASONS)[number];

/**
 * The wire type is deliberately WIDER than the four known reasons: a server
 * that learns a fifth rule must degrade to a generic "looks stale" rendering
 * on older clients, never crash and never drop the row (CLAUDE.md "API
 * Response Compatibility" → enum drift downgrades, not crashes).
 */
export type StaleReason = KnownStaleReason | (string & {});

export interface StaleIssue {
  issue_id: string;
  identifier: string;
  title: string;
  /** Issue status as of the computation. Widened for the same drift reason. */
  status: string;
  reason: StaleReason;
  /** ISO timestamp the rule measured from — what the age in the UI counts. */
  since: string;
}

export interface StaleIssuesResponse {
  stale: StaleIssue[];
}
