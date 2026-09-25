import type { Issue, IssueMetadata, IssueReaction } from "./issue";
import type { Agent } from "./agent";
import type { InboxItem } from "./inbox";
import type { Escalation } from "./escalation";
import type { Comment, Reaction } from "./comment";
import type { TimelineEntry } from "./activity";
import type { Workspace, MemberWithUser, Invitation } from "./workspace";
import type { Project } from "./project";
import type { Label } from "./label";
import type { KnowledgeUpdatedPayload } from "../knowledge/types";

// WebSocket event types (matching Go server protocol/events.go)
export type WSEventType =
  | "issue:created"
  | "issue:updated"
  | "issue:deleted"
  | "comment:created"
  | "comment:updated"
  | "comment:deleted"
  | "comment:resolved"
  | "comment:unresolved"
  | "agent:status"
  | "agent:created"
  | "agent:archived"
  | "agent:restored"
  | "task:queued"
  | "task:dispatch"
  | "task:running"
  | "task:waiting_local_directory"
  | "task:waiting_human"
  | "task:progress"
  | "task:completed"
  | "task:failed"
  | "task:message"
  | "task:cancelled"
  | "orchestration:changed"
  | "escalation:opened"
  | "escalation:resolved"
  | "inbox:new"
  | "inbox:read"
  | "inbox:archived"
  | "inbox:batch-read"
  | "inbox:batch-archived"
  | "workspace:updated"
  | "workspace:deleted"
  | "member:added"
  | "member:updated"
  | "member:removed"
  | "daemon:heartbeat"
  | "daemon:register"
  | "skill:created"
  | "skill:updated"
  | "skill:deleted"
  | "subscriber:added"
  | "subscriber:removed"
  | "activity:created"
  | "reaction:added"
  | "reaction:removed"
  | "issue_reaction:added"
  | "issue_reaction:removed"
  | "chat:message"
  | "chat:done"
  | "chat:session_read"
  | "chat:session_deleted"
  | "chat:session_updated"
  | "assistant:message"
  | "assistant:tool_activity"
  | "assistant:run_finished"
  | "project:created"
  | "project:updated"
  | "project:deleted"
  | "squad:created"
  | "squad:updated"
  | "squad:deleted"
  | "label:created"
  | "label:updated"
  | "label:deleted"
  | "knowledge:updated"
  | "issue_labels:changed"
  | "issue_metadata:changed"
  | "qa_evidence:ready"
  | "test_cases:changed"
  | "report:pinned"
  | "report:unpinned"
  | "report:updated"
  | "report:schedule_changed"
  | "pin:created"
  | "pin:deleted"
  | "pin:reordered"
  | "invitation:created"
  | "invitation:accepted"
  | "invitation:declined"
  | "invitation:revoked"
  | "github_installation:created"
  | "github_installation:deleted"
  | "pull_request:linked"
  | "pull_request:updated"
  | "pull_request:unlinked";

export interface WSMessage<T = unknown> {
  type: WSEventType;
  payload: T;
  actor_id?: string;
  actor_type?: string;
}

export interface IssueCreatedPayload {
  issue: Issue;
}

export interface IssueUpdatedPayload {
  issue: Issue;
}

export interface IssueDeletedPayload {
  issue_id: string;
}

export interface IssueLabelsChangedPayload {
  issue_id: string;
  labels: Label[];
}

export interface IssueMetadataChangedPayload {
  issue_id: string;
  metadata: IssueMetadata;
}

/**
 * Broadcast after a persisted orchestration run, step, or plan transition.
 * `issue_id` selects the exact React Query cache entry while `run_id` lets
 * consumers reject or diagnose events from superseded runs.
 */
export interface OrchestrationChangedPayload {
  issue_id: string;
  run_id: string;
  step_id?: string;
  kind: string;
  event_id?: string;
  plan_version?: number;
}

/**
 * escalation:opened / escalation:resolved — an agent stopped and asked, or a
 * human answered. Workspace-scoped: an escalation names an issue, and issues
 * are tenanted. Carries the whole escalation so one payload can refresh the
 * issue card, the inbox and the decision queue.
 */
export interface EscalationEventPayload {
  issue_id: string;
  escalation: Escalation;
}

export interface AgentStatusPayload {
  agent: Agent;
}

export interface AgentCreatedPayload {
  agent: Agent;
}

export interface AgentArchivedPayload {
  agent: Agent;
}

export interface AgentRestoredPayload {
  agent: Agent;
}

export interface InboxNewPayload {
  item: InboxItem;
}

export interface InboxReadPayload {
  item_id: string;
  recipient_id: string;
}

export interface InboxArchivedPayload {
  item_id: string;
  recipient_id: string;
}

export interface InboxBatchReadPayload {
  recipient_id: string;
  count: number;
}

export interface InboxBatchArchivedPayload {
  recipient_id: string;
  count: number;
}

export interface CommentCreatedPayload {
  comment: Comment;
}

export interface CommentUpdatedPayload {
  comment: Comment;
}

export interface CommentDeletedPayload {
  comment_id: string;
  issue_id: string;
}

export interface CommentResolvedPayload {
  comment: Comment;
}

export interface CommentUnresolvedPayload {
  comment: Comment;
}

export interface WorkspaceUpdatedPayload {
  workspace: Workspace;
}

export interface WorkspaceDeletedPayload {
  workspace_id: string;
}

export interface MemberUpdatedPayload {
  member: MemberWithUser;
}

export interface MemberAddedPayload {
  member: MemberWithUser;
  workspace_id: string;
  workspace_name?: string;
}

export interface MemberRemovedPayload {
  member_id: string;
  user_id: string;
  workspace_id: string;
}

export interface SubscriberAddedPayload {
  issue_id: string;
  user_type: string;
  user_id: string;
  reason: string;
}

export interface SubscriberRemovedPayload {
  issue_id: string;
  user_type: string;
  user_id: string;
}

export interface ActivityCreatedPayload {
  issue_id: string;
  entry: TimelineEntry;
}

export interface TaskMessagePayload {
  task_id: string;
  issue_id: string;
  chat_session_id?: string;
  seq: number;
  type: "text" | "thinking" | "tool_use" | "tool_result" | "error";
  tool?: string;
  content?: string;
  input?: Record<string, unknown>;
  output?: string;
  created_at?: string;
}

export interface TaskQueuedPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  chat_session_id?: string;
  status: string;
}

export interface TaskDispatchPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  runtime_id: string;
  chat_session_id?: string;
}

export interface TaskRunningPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  chat_session_id?: string;
  status: string;
}

// task:waiting_local_directory fires when the daemon dequeues a task but
// can't immediately acquire the on-disk path lock — another task on this
// daemon is already executing in the same local_directory. The optional
// `wait_reason` mirrors the server-side hint (path / holder task id), but
// is not yet surfaced end-to-end; the UI today only reads the status.
export interface TaskWaitingLocalDirectoryPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  chat_session_id?: string;
  status: string;
  wait_reason?: string;
}

// task:waiting_human fires when an agent raised an escalation and the server
// parked its run. Unlike waiting_local_directory — a daemon-owned hold that
// clears in seconds — this one is owned by a PERSON and may sit for a day, so
// the runtime slot is freed rather than held. `wait_reason` carries the
// escalation's one-sentence ask.
export interface TaskWaitingHumanPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  chat_session_id?: string;
  status: string;
  wait_reason?: string;
}

export interface TaskCompletedPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  chat_session_id?: string;
  status: string;
}

export interface TaskFailedPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  chat_session_id?: string;
  status: string;
}

export interface TaskCancelledPayload {
  task_id: string;
  agent_id: string;
  issue_id: string;
  chat_session_id?: string;
  status: string;
}

export interface ReactionAddedPayload {
  reaction: Reaction;
  issue_id: string;
}

export interface ReactionRemovedPayload {
  comment_id: string;
  issue_id: string;
  emoji: string;
  actor_type: string;
  actor_id: string;
}

export interface IssueReactionAddedPayload {
  reaction: IssueReaction;
  issue_id: string;
}

export interface IssueReactionRemovedPayload {
  issue_id: string;
  emoji: string;
  actor_type: string;
  actor_id: string;
}

export interface ChatMessageEventPayload {
  chat_session_id: string;
  message_id: string;
  role: "user" | "assistant";
  content: string;
  task_id?: string;
  created_at: string;
}

export interface ChatDonePayload {
  chat_session_id: string;
  task_id: string;
  /**
   * Server populates these from the freshly-persisted assistant ChatMessage
   * row so the WS handler can write it into the messages cache inline. Older
   * servers (pre-#2123) only sent chat_session_id + task_id; treat every field
   * below as optional and fall back to a refetch when absent.
   */
  message_id?: string;
  content?: string;
  elapsed_ms?: number;
  created_at?: string;
}

export interface ChatSessionReadPayload {
  chat_session_id: string;
}

export interface ChatSessionDeletedPayload {
  chat_session_id: string;
}

/**
 * Announces one persisted Agora Assistant transcript row (an assistant reply,
 * or a tool answer). The assistant is user-scoped (no workspace room), so the
 * payload carries `user_id` directly — see server/pkg/protocol/messages.go
 * AssistantMessagePayload.
 */
/**
 * Pinned reports (docs/assistant-domain-plan.md Phase 2a + 2b).
 * Workspace-scoped, unlike the assistant's own user-scoped events:
 * `report:pinned` / `report:unpinned` fire on publish and withdrawal,
 * `report:updated` when the owner re-runs a recipe and the pinned artifact's
 * body moves behind an unchanged pin, and `report:schedule_changed` when the
 * owner sets or clears the pin's refresh cadence (2b) — all four move the same
 * caches, so they share one payload and one updater.
 */
export interface ReportPinEventPayload {
  project_id: string;
  pin_id: string;
  /** Present on the 2b schedule event; the older three omit it. */
  artifact_id?: string;
}

export interface AssistantMessageEventPayload {
  user_id: string;
  session_id: string;
  run_id: string;
  message_id: string;
  role: "user" | "assistant" | "tool";
  content?: string;
  tool_name?: string;
  created_at: string;
}

/**
 * Emitted the moment a tool call starts, before it has a result — drives the
 * live "Searching issues…" working indicator. Ephemeral: never written to the
 * Query cache, only to transient UI state.
 */
export interface AssistantToolActivityEventPayload {
  user_id: string;
  session_id: string;
  run_id: string;
  tool_call_id: string;
  tool_name: string;
}

/** Closes an Agora Assistant run. `error` is set only when status is "failed". */
export interface AssistantRunFinishedPayload {
  user_id: string;
  session_id: string;
  run_id: string;
  status: "ok" | "failed" | "cancelled";
  error?: string;
}

export interface ProjectCreatedPayload {
  project: Project;
}

export interface ProjectUpdatedPayload {
  project: Project;
}

export interface ProjectDeletedPayload {
  project_id: string;
}

export interface InvitationCreatedPayload {
  invitation: Invitation;
  workspace_name?: string;
}

export interface InvitationAcceptedPayload {
  invitation_id: string;
  member: MemberWithUser;
}

export interface InvitationDeclinedPayload {
  invitation_id: string;
  invitee_email: string;
}

export interface InvitationRevokedPayload {
  invitation_id: string;
  invitee_email: string;
}

/**
 * Maps every WSEventType to its payload interface. Events whose payload
 * shape isn't formally typed (server emits an object the client doesn't
 * meaningfully consume yet) fall back to `unknown` — callers must narrow
 * before access.
 *
 * Use via `WSEventPayload<E>` rather than indexing the map directly:
 *   const handler = (payload: WSEventPayload<"issue:created">) => { ... };
 *
 * Adding a new event: extend WSEventType first (above), then append a key
 * here. TS will compile-error every WSClient.on("new:event", …) site that
 * forgets the payload shape — that's the whole point.
 */
export interface WSEventPayloadMap {
  "issue:created": IssueCreatedPayload;
  "issue:updated": IssueUpdatedPayload;
  "issue:deleted": IssueDeletedPayload;
  "issue_labels:changed": IssueLabelsChangedPayload;
  "issue_reaction:added": IssueReactionAddedPayload;
  "issue_reaction:removed": IssueReactionRemovedPayload;
  "comment:created": CommentCreatedPayload;
  "comment:updated": CommentUpdatedPayload;
  "comment:deleted": CommentDeletedPayload;
  "comment:resolved": CommentResolvedPayload;
  "comment:unresolved": CommentUnresolvedPayload;
  "reaction:added": ReactionAddedPayload;
  "reaction:removed": ReactionRemovedPayload;
  "agent:status": AgentStatusPayload;
  "agent:created": AgentCreatedPayload;
  "agent:archived": AgentArchivedPayload;
  "agent:restored": AgentRestoredPayload;
  "task:queued": TaskQueuedPayload;
  "task:dispatch": TaskDispatchPayload;
  "task:running": TaskRunningPayload;
  "task:waiting_local_directory": TaskWaitingLocalDirectoryPayload;
  "task:waiting_human": TaskWaitingHumanPayload;
  "task:completed": TaskCompletedPayload;
  "task:failed": TaskFailedPayload;
  "task:message": TaskMessagePayload;
  "task:cancelled": TaskCancelledPayload;
  "task:progress": unknown;
  "orchestration:changed": OrchestrationChangedPayload;
  "escalation:opened": EscalationEventPayload;
  "escalation:resolved": EscalationEventPayload;
  "inbox:new": InboxNewPayload;
  "inbox:read": InboxReadPayload;
  "inbox:archived": InboxArchivedPayload;
  "inbox:batch-read": InboxBatchReadPayload;
  "inbox:batch-archived": InboxBatchArchivedPayload;
  "workspace:updated": WorkspaceUpdatedPayload;
  "workspace:deleted": WorkspaceDeletedPayload;
  "member:added": MemberAddedPayload;
  "member:updated": MemberUpdatedPayload;
  "member:removed": MemberRemovedPayload;
  "subscriber:added": SubscriberAddedPayload;
  "subscriber:removed": SubscriberRemovedPayload;
  "activity:created": ActivityCreatedPayload;
  "chat:message": ChatMessageEventPayload;
  "chat:done": ChatDonePayload;
  "chat:session_read": ChatSessionReadPayload;
  "chat:session_deleted": ChatSessionDeletedPayload;
  "chat:session_updated": unknown;
  "assistant:message": AssistantMessageEventPayload;
  "assistant:tool_activity": AssistantToolActivityEventPayload;
  "assistant:run_finished": AssistantRunFinishedPayload;
  "project:created": ProjectCreatedPayload;
  "project:updated": ProjectUpdatedPayload;
  "project:deleted": ProjectDeletedPayload;
  "invitation:created": InvitationCreatedPayload;
  "invitation:accepted": InvitationAcceptedPayload;
  "invitation:declined": InvitationDeclinedPayload;
  "invitation:revoked": InvitationRevokedPayload;
  // No formal payload interfaces yet — server emits domain objects clients
  // currently consume as opaque triggers (refetch on receipt).
  "daemon:heartbeat": unknown;
  "daemon:register": unknown;
  "skill:created": unknown;
  "skill:updated": unknown;
  "skill:deleted": unknown;
  "squad:created": unknown;
  "squad:updated": unknown;
  "squad:deleted": unknown;
  "label:created": unknown;
  "label:updated": unknown;
  "label:deleted": unknown;
  "knowledge:updated": KnowledgeUpdatedPayload;
  "report:pinned": ReportPinEventPayload;
  "report:unpinned": ReportPinEventPayload;
  "report:updated": ReportPinEventPayload;
  "report:schedule_changed": ReportPinEventPayload;
  "pin:created": unknown;
  "pin:deleted": unknown;
  "pin:reordered": unknown;
  "github_installation:created": unknown;
  "github_installation:deleted": unknown;
  "pull_request:linked": unknown;
  "pull_request:updated": unknown;
  "pull_request:unlinked": unknown;
}

/**
 * Payload type for a given event. Lookup against WSEventPayloadMap with
 * `unknown` as the safety net — if a future WSEventType is added without
 * a map entry, callers see `unknown` (forced narrow) rather than `any`
 * (silent unsafe access).
 */
export type WSEventPayload<E extends WSEventType> =
  E extends keyof WSEventPayloadMap ? WSEventPayloadMap[E] : unknown;
