package protocol

// Event payload keys shared by publishers and in-process listeners.
const (
	// EventPayloadSuppressExternalNotifications marks a dev/test event that
	// must still reach websocket/cache subscribers but must not fan out to
	// external systems such as Telegram. Production HTTP requests never set it.
	EventPayloadSuppressExternalNotifications = "suppress_external_notifications"
)

// Event types for WebSocket communication between server, web clients, and daemon.
const (
	// Issue events
	EventIssueCreated         = "issue:created"
	EventIssueUpdated         = "issue:updated"
	EventIssueDeleted         = "issue:deleted"
	EventIssueMetadataChanged = "issue_metadata:changed"

	// Comment events
	EventCommentCreated       = "comment:created"
	EventCommentUpdated       = "comment:updated"
	EventCommentDeleted       = "comment:deleted"
	EventCommentResolved      = "comment:resolved"
	EventCommentUnresolved    = "comment:unresolved"
	EventReactionAdded        = "reaction:added"
	EventReactionRemoved      = "reaction:removed"
	EventIssueReactionAdded   = "issue_reaction:added"
	EventIssueReactionRemoved = "issue_reaction:removed"

	// Agent events
	EventAgentStatus   = "agent:status"
	EventAgentCreated  = "agent:created"
	EventAgentArchived = "agent:archived"
	EventAgentRestored = "agent:restored"

	// Task events (server <-> daemon).
	// Each event maps to a status transition on agent_task_queue. Front-end
	// subscribes by `task:` prefix and invalidates the workspace task
	// snapshot, so the granularity here is "what does the user want to see
	// change" — not "every internal status flip".
	EventTaskQueued                = "task:queued"                  // ∅ → queued (enqueue / retry create)
	EventTaskDispatch              = "task:dispatch"                // queued → dispatched (daemon claim)
	EventTaskRunning               = "task:running"                 // dispatched → running (daemon started)
	EventTaskWaitingLocalDirectory = "task:waiting_local_directory" // dispatched → waiting_local_directory (daemon parked on a busy local_directory path)
	EventTaskProgress              = "task:progress"
	EventTaskCompleted             = "task:completed" // running → completed
	EventTaskFailed                = "task:failed"    // running → failed
	EventTaskMessage               = "task:message"
	EventTaskCancelled             = "task:cancelled" // * → cancelled
	// EventTaskWaitingHuman: running → waiting_human. The agent raised an
	// escalation; the run ENDED and the task is parked until a person
	// answers. Unlike waiting_local_directory (a daemon-owned hold that
	// clears in seconds) this one is owned by a human and may sit for a day,
	// so the runtime slot is freed rather than held.
	EventTaskWaitingHuman = "task:waiting_human"

	// Orchestration events. This is a cache-coherence signal for persisted
	// plan/run/step transitions; the HTTP orchestration response remains the
	// authoritative state returned to clients.
	EventOrchestrationChanged = "orchestration:changed"

	// Escalation events (docs/orchestration-upgrade-plan.md §B1).
	// Workspace-scoped: an escalation names an issue, and issues are
	// tenanted. Both carry {issue_id, escalation} so a client can update the
	// issue card, the inbox and the decision queue from one payload.
	EventEscalationOpened   = "escalation:opened"   // an agent stopped and asked
	EventEscalationResolved = "escalation:resolved" // answered or cancelled by a human

	// Inbox events
	EventInboxNew           = "inbox:new"
	EventInboxRead          = "inbox:read"
	EventInboxArchived      = "inbox:archived"
	EventInboxBatchRead     = "inbox:batch-read"
	EventInboxBatchArchived = "inbox:batch-archived"

	// Workspace events
	EventWorkspaceUpdated = "workspace:updated"
	EventWorkspaceDeleted = "workspace:deleted"

	// Member events
	EventMemberAdded   = "member:added"
	EventMemberUpdated = "member:updated"
	EventMemberRemoved = "member:removed"

	// Subscriber events
	EventSubscriberAdded   = "subscriber:added"
	EventSubscriberRemoved = "subscriber:removed"

	// Activity events
	EventActivityCreated = "activity:created"

	// Skill events
	EventSkillCreated = "skill:created"
	EventSkillUpdated = "skill:updated"
	EventSkillDeleted = "skill:deleted"

	// Chat events
	EventChatMessage        = "chat:message"
	EventChatDone           = "chat:done"
	EventChatSessionRead    = "chat:session_read"
	EventChatSessionDeleted = "chat:session_deleted"
	EventChatSessionUpdated = "chat:session_updated"

	// Agora Assistant events. User-scoped (the whole feature is), so these are
	// PERSONAL events — the realtime layer addresses them to the one user the
	// payload names and never broadcasts them to a workspace room.
	EventAssistantMessage      = "assistant:message"
	EventAssistantToolActivity = "assistant:tool_activity"
	EventAssistantRunFinished  = "assistant:run_finished"

	// Pinned report events (docs/assistant-domain-plan.md §Phase 2a). A pin
	// publishes one user-owned assistant artifact into a project, so unlike
	// the assistant events above these are WORKSPACE-scoped: everyone who can
	// see the project sees the report. Payloads carry ids only — the reader
	// refetches through the membership-gated endpoints, so a fanout can never
	// become the thing that leaks a report's contents.
	//
	// report:updated fires from the artifact write path, once per workspace
	// the artifact is pinned into, because a refreshed report is the ONLY
	// change a project page cannot learn about from its own surface.
	EventReportPinned   = "report:pinned"
	EventReportUnpinned = "report:unpinned"
	EventReportUpdated  = "report:updated"

	// report:schedule_changed fires when a report's standing refresh cadence is
	// set, replaced or cleared (docs/assistant-domain-plan.md §Phase 2b). Same
	// workspace scope and same ids-only payload as the three above: the cadence
	// badge on the project page is the only thing that moves, and it refetches
	// through the membership-gated list. The scheduler itself is silent — a run
	// it starts already announces itself through report:updated when the
	// artifact is rewritten, so a second event would only tell listeners the
	// same thing twice.
	EventReportScheduleChanged = "report:schedule_changed"

	// Project events
	EventProjectCreated         = "project:created"
	EventProjectUpdated         = "project:updated"
	EventProjectDeleted         = "project:deleted"
	EventProjectResourceCreated = "project_resource:created"
	EventProjectResourceUpdated = "project_resource:updated"
	EventProjectResourceDeleted = "project_resource:deleted"

	// Label events
	EventLabelCreated       = "label:created"
	EventLabelUpdated       = "label:updated"
	EventLabelDeleted       = "label:deleted"
	EventIssueLabelsChanged = "issue_labels:changed"

	// Sprint events
	EventSprintCreated      = "sprint:created"
	EventSprintUpdated      = "sprint:updated"
	EventSprintDeleted      = "sprint:deleted"
	EventIssueSprintChanged = "issue_sprint:changed"

	// Release lifecycle events (release-hub Thread B). deploy:recorded fires
	// after any deploy_event row is persisted (a QA-box git-sync OR a pipeline
	// deploy-result); release:shipped fires when a deploy to a production-tier
	// (requires_human / production-named) environment SUCCEEDS — the seam the
	// release-integrations dispatcher fans out to configured connectors.
	EventDeployRecorded = "deploy:recorded"
	EventReleaseShipped = "release:shipped"

	// QA evidence — a run_qa verdict was parsed + persisted for an issue.
	EventQAEvidenceReady = "qa_evidence:ready"

	// QA test cases changed for an issue (agent authored, or a run recorded).
	EventTestCasesChanged = "test_cases:changed"

	// Code review landed a verdict on an issue. Published at the one place a
	// verdict is acted on (handler/review_outcome.go's onReviewVerdictLabel),
	// so every ingress — CLI label attach, HTTP verdict comment capture,
	// task-completion capture — announces it exactly once.
	//
	// Added because review outcomes used to reach external systems through a
	// DIRECT call only (SendReviewVerdictGroupNotify), which meant any
	// listener that subscribed to the bus silently missed them. Payload is
	// ids plus the verdict word: a subscriber refetches through the
	// membership-gated endpoints, so a fanout can never become the thing that
	// leaks an issue's contents.
	EventReviewVerdict = "review:verdict"

	// Knowledge items changed / KB recompiled (structured knowledge flywheel).
	EventKnowledgeChanged = "knowledge:changed"

	// Pin events
	EventPinCreated   = "pin:created"
	EventPinDeleted   = "pin:deleted"
	EventPinReordered = "pin:reordered"

	// Invitation events
	EventInvitationCreated  = "invitation:created"
	EventInvitationAccepted = "invitation:accepted"
	EventInvitationDeclined = "invitation:declined"
	EventInvitationRevoked  = "invitation:revoked"

	// Autopilot events
	EventAutopilotCreated  = "autopilot:created"
	EventAutopilotUpdated  = "autopilot:updated"
	EventAutopilotDeleted  = "autopilot:deleted"
	EventAutopilotRunStart = "autopilot:run_start"
	EventAutopilotRunDone  = "autopilot:run_done"

	// Automation events (user-defined task rules). One event per EVALUATION that
	// reached a rule — applied, skipped or failed — so the flow editor's run
	// history and the list's counters stay live without polling.
	EventAutomationRun = "automation:run"

	// Squad events
	EventSquadCreated = "squad:created"
	EventSquadUpdated = "squad:updated"
	EventSquadDeleted = "squad:deleted"

	// Daemon events
	EventDaemonHeartbeat     = "daemon:heartbeat"
	EventDaemonHeartbeatAck  = "daemon:heartbeat_ack"
	EventDaemonRegister      = "daemon:register"
	EventDaemonTaskAvailable = "daemon:task_available"

	// GitHub integration events
	EventGitHubInstallationCreated = "github_installation:created"
	EventGitHubInstallationDeleted = "github_installation:deleted"
	EventPullRequestLinked         = "pull_request:linked"
	EventPullRequestUpdated        = "pull_request:updated"
	EventPullRequestUnlinked       = "pull_request:unlinked"

	// Lark integration events. `created` covers both first-install
	// (UNIQUE on (workspace_id, agent_id) means at most one row per
	// agent) and re-install via UpsertLarkInstallation — front-ends
	// treat both as a single "installation appeared / refreshed"
	// notification. `revoked` flips status to 'revoked' without
	// deleting the row; the audit trail is preserved.
	EventLarkInstallationCreated = "lark_installation:created"
	EventLarkInstallationRevoked = "lark_installation:revoked"

	// Tracker import progress (docs/importers-plan.md §3.7). Workspace-scoped
	// and THROTTLED at the source — the Runner emits a tick every N rows and
	// every 2s, whichever is slower, so a 10k-issue import is not 10k frames.
	// The payload carries counters only; the authoritative state is the
	// import_job row, which the client reads through the membership-gated
	// GET /api/workspaces/{id}/import/jobs/{jid}.
	EventImportProgress = "import:progress"
)
