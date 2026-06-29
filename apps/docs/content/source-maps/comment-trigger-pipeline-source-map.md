Page: comment-trigger-pipeline (developers/agentic). Every claim grounded in source read during this session.

CORE FLOW — server/internal/handler/comment.go:
- triggerTasksForComment (L1057-1064): isNoteComment early-out -> computeCommentAgentTriggers -> filterSuppressedCommentAgentTriggers -> enqueueCommentAgentTriggers. Called from CreateComment (L1034, with suppressAgentIDs) and UpdateComment (L1549, with nil) only when oldContent != comment.Content (L1532).
- isNoteComment (L1048-1055), noteCommentPrefix = "/note" (L1042). First whitespace-delimited token, case-insensitive (strings.EqualFold).
- computeCommentAgentTriggers (L1124-1160): re-checks isNoteComment at top (L1125-1127). `add` closure dedupes by uuidToString(trigger.Agent.ID) via `seen` map (L1131-1138). Order: assignee (L1140-1149), assigned-squad-leader (L1151-1153), mentioned agents (L1155-1157).

SOURCE 1 — assignee wake (L1140-1149): actorType=="member" AND shouldEnqueueOnComment AND !commentMentionsOthersButNotAssignee AND !isReplyToMemberThread. Loads via GetAgentInWorkspace(issue.AssigneeID). Source=commentTriggerSourceIssueAssignee, nil Squad.
- shouldEnqueueOnComment (issue.go L2732-2754): assignee_type=="agent", AssigneeID valid, agent has RuntimeID + not archived, canAccessPrivateAgent, !HasPendingTaskForIssueAndAgent.

SOURCE 2 — computeAssignedSquadLeaderCommentTrigger (comment.go L1162-1198): requires AssigneeType=="squad". GetSquadInWorkspace. Guard A (L1173-1176): authorType=="agent" && authorID==squad.LeaderID && lastTaskWasLeader -> false. Guard B (L1177-1179): authorType=="member" && commentMentionsAnyone(content) -> false. Readiness L1184 (!RuntimeID.Valid || ArchivedAt.Valid). canAccessPrivateAgent L1187. HasPendingTaskForIssueAndAgent L1190-1196. Returns Source=commentTriggerSourceIssueAssignee with NON-NIL Squad (L1197).
- commentMentionsAnyone (squad.go L931-939): types agent|member|squad|all return true; issue ignored. Only current comment, no parent inheritance.
- lastTaskWasLeader (squad.go L915-924): GetLatestTaskIsLeaderForIssueAndAgent.

SOURCE 3 — computeMentionedAgentCommentTriggers (comment.go L1335-1427): ParseMentions(content); shouldInheritParentMentions (L1306-1317: parent!=nil, reply 0 mentions, reply author not agent, parent author member) -> use parent mentions. @squad -> leader, Source=commentTriggerSourceMentionSquadLeader (L1391). @agent -> Source=commentTriggerSourceMentionAgent (L1424). Same self-trigger/readiness/visibility/dedup gates. No status gate (docstring L1333-1334).

commentMentionsOthersButNotAssignee (comment.go L1206-1233): drops issue-type mentions; no mentions->false; HasMentionAll->true; no AssigneeID->true; assignee in mentions->false; else->true. Governs Source 1 only.

SUPPRESSION — filterSuppressedCommentAgentTriggers (L1066-1087): drops triggers whose Agent.ID in suppressAgentIDs set. Runs after compute, before enqueue. CreateCommentRequest.SuppressAgentIDs (L775).

ENQUEUE — enqueueCommentAgentTriggers (L1089-1122): switch trigger.Source. IssueAssignee + Squad!=nil -> EnqueueTaskForSquadLeader (L1094); IssueAssignee + nil -> EnqueueTaskForIssue (L1103); MentionSquadLeader -> EnqueueTaskForSquadLeader (L1107); MentionAgent -> EnqueueTaskForMention (L1114). All failures slog.Warn + continue.
- task.go: EnqueueTaskForIssue (L465), EnqueueTaskForMention (L539, isLeader=false), EnqueueTaskForSquadLeader (L549, isLeader=true) -> enqueueMentionTask (L553) stamps IsLeaderTask (L575). TriggerCommentID + TriggerSummary via buildCommentTriggerSummary (L95-108) truncateForSummary (L61-81) triggerSummaryMaxLen=200 (L55).

PREVIEW — PreviewCommentTriggers (comment.go L832-877): POST /comments/trigger-preview (verified test L56). CommentTriggerPreviewRequest (L778). Loads issue, resolveActor (L870), mention.ExpandIssueIdentifiers (L864), empty content short-circuits (L865-868), calls computeCommentAgentTriggers (L871) — NO suppression filter. Maps to CommentTriggerAgentResponse (L822-830) with source + commentAgentTriggerReason (L809-820).

NO_ACTION 409 GATE — CreateComment (comment.go L944-971): authorType=="agent", X-Task-ID header, GetAgentTask, task.IssueID matches issue.ID. (1) parent-drift: if task.TriggerCommentID.Valid && parentID != it -> 409 "parent_id must equal this task's trigger comment id (...)" (L950-955). (2) HasSquadLeaderNoActionEvaluationForTask (L957) -> if noAction 409 "squad leader recorded no_action; comments are not allowed for this task" (L964-966). checkErr logged but not blocking (L958-963) = fails open on infra error.
- HasSquadLeaderNoActionEvaluationForTask (squad_no_action.go L12-21): keys on IssueID, AgentID, TaskID.

util/mention.go: ParseMentions (L24), HasMentionAll (L40), Mention (L6) types member/agent/squad/issue/all.

CONVENTIONS: frontmatter title+description; imports Callout + Mermaid only; first block Callout Audience=both; Source files table at end + same-PR drift sentence. Sibling links used: mention-contract, squads-routing, no-action-protocol, loop-prevention, conventions (all in allowed list). All angle brackets in prose are inside backticks/fences.