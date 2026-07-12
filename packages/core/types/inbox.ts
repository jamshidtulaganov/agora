import type { IssueStatus } from "./issue";

export type InboxSeverity = "action_required" | "attention" | "info";

export type InboxItemType =
  | "issue_assigned"
  | "unassigned"
  | "assignee_changed"
  | "status_changed"
  | "priority_changed"
  | "start_date_changed"
  | "due_date_changed"
  | "new_comment"
  | "mentioned"
  | "review_requested"
  | "task_completed"
  | "task_failed"
  | "agent_blocked"
  | "agent_completed"
  | "reaction_added"
  | "quick_create_done"
  | "quick_create_failed"
  | "design_proposal_ready"
  | "design_proposal_blocked"
  // QA verdict notifications (Phase 2 — the human-first loop): a qa:fail
  // landing on an issue, and a qa:pass that RECOVERS from a prior fail.
  // A routine (non-recovery) pass never notifies.
  | "qa_failed"
  | "qa_passed"
  // Review stage v2 ("agent reviews, human approves"): review:fail landing
  // on an issue (action_required), a review:pass that leaves every required
  // merge gate green (merge_ready, action_required — "awaiting your
  // approval"), and a routine review:pass (info).
  | "review_failed"
  | "review_passed"
  | "merge_ready";

export interface InboxItem {
  id: string;
  workspace_id: string;
  recipient_type: "member" | "agent";
  recipient_id: string;
  actor_type: "member" | "agent" | "system" | null;
  actor_id: string | null;
  type: InboxItemType;
  severity: InboxSeverity;
  issue_id: string | null;
  title: string;
  body: string | null;
  issue_status: IssueStatus | null;
  read: boolean;
  archived: boolean;
  created_at: string;
  details: Record<string, string> | null;
}
