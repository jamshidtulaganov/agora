## no-action-protocol — source map

Every claim on the page is grounded in code read during authoring. Verbatim symbols and their exact source locations:

### server/internal/service/squad_no_action.go
- `HasSquadLeaderNoActionEvaluationForTask(ctx context.Context, q *db.Queries, task db.AgentTaskQueue) (bool, error)` — service wrapper (lines 12-21).
- Nil/invalid guard returns `false, nil` when `q == nil || !task.ID.Valid || !task.IssueID.Valid || !task.AgentID.Valid` (line 13).
- Delegates to generated query with `IssueID: task.IssueID`, `AgentID: task.AgentID`, `TaskID: util.UUIDToString(task.ID)` (lines 16-20). → page's "(issue_id, leader agent_id, task_id)" triple.

### server/internal/handler/squad.go
- `RecordSquadLeaderEvaluation` (lines 811-904).
- `req.Outcome` server-side validation; `400` message `"outcome must be 'action', 'no_action', or 'failed'"` (lines 826-829).
- Squad-assignment check `issue.AssigneeType.String != "squad"` → `400 "issue is not assigned to a squad"` (lines 832-835).
- `GetSquadInWorkspace` scoped to `issue.WorkspaceID`; `404 "squad not found"` (lines 837-844).
- `resolveActor` → `(actorType, actorID)`; leader-only check `actorType != "agent" || actorID != uuidToString(squad.LeaderID)` → `403 "only the squad leader agent can record evaluations"` (lines 849-853). Verbatim code block on page copied from lines 850-853.
- `X-Task-ID` via `r.Header.Get("X-Task-ID")` + `parseUUIDOrBadRequest(w, taskID, "task id")` → `400` (lines 855-859).
- `GetAgentTask` + cross-issue check `uuidToString(task.IssueID) != uuidToString(issue.ID)` → `400 "task does not belong to issue"` (lines 860-864). Verbatim code block copied from lines 861-864.
- `CreateActivity` with `Action: "squad_leader_evaluated"` and `details` marshaled from `{squad_id, task_id, outcome, reason}` (lines 866-880); `201` response, no comment created (lines 873-903).
- `EventActivityCreated` publish (lines 886-897).

### server/internal/service/task.go
- Completion path (lines 1296-1332): `suppressNoActionComment, err := HasSquadLeaderNoActionEvaluationForTask(ctx, s.Queries, task)` (line 1297).
- Error path logs warning, leaves `suppressNoActionComment` false → fails open (lines 1298-1305). → page's "fails open" note.
- `HasAgentCommentedSince` with `IssueID/AuthorID=task.AgentID/Since=task.StartedAt` (lines 1306-1310).
- Guard `if !suppressNoActionComment && !agentCommented { ... synthesize ... }` (line 1311); synthesis writes from `payload.Output` via `createAgentComment` (lines 1312-1330). → suppression #1.
- Invariant comment block lines 1288-1295 ("every completed issue task must have at least one agent comment").

### server/internal/handler/comment.go
- Agent-author branch keyed on `authorType == "agent"` and `X-Task-ID` (lines 944-971).
- `parent_id` resumed-session guard returns `409` when `parentID != task.TriggerCommentID` (lines 950-956). → page's adjacent-guard note.
- `service.HasSquadLeaderNoActionEvaluationForTask` → on `noAction` `writeError(w, http.StatusConflict, "squad leader recorded no_action; comments are not allowed for this task")` (lines 957-967). Verbatim message + `409`. → suppression #2.

### server/pkg/db/queries/activity.sql
- `HasSquadLeaderNoActionEvaluationForTask :one` `EXISTS` query (lines 19-29). Predicate: `issue_id = @issue_id AND actor_type = 'agent' AND actor_id = @agent_id AND action = 'squad_leader_evaluated' AND details->>'outcome' = 'no_action' AND details->>'task_id' = @task_id::text`. SQL block on page copied verbatim. Generated form in server/pkg/db/generated/activity.sql.go lines 121-140.

### server/cmd/agora/cmd_squad.go
- `squadActivityCmd` `Use: "activity <issue-id> <outcome>"`, `Args: exactArgs(2)` (lines 442-456).
- Long help lists the three outcomes and "intended to be called by squad leader agents after each trigger" (lines 445-453).
- `runSquadActivity`: outcome re-validation, `resolveIssueRef`, body `{outcome, reason}`, `POST /api/issues/<id>/squad-evaluated` (lines 458-497, post at line 486).
- `--reason` flag registered (line 546): `String("reason", "", "Short explanation of the decision")`.

### server/internal/cli/client.go
- `TaskID` field documented "When set, sent as X-Task-ID for agent-task validation" (line 54); `req.Header.Set("X-Task-ID", c.TaskID)` (line 175). → page's "CLI stamps this header automatically".

### server/cmd/server/router.go
- Route registration `r.Post("/api/issues/{id}/squad-evaluated", h.RecordSquadLeaderEvaluation)` (line 889). Confirms endpoint path used by CLI and handler match.

### Cross-links used (all verified to exist in apps/docs/content/docs/developers/agentic/)
- squads-routing, comment-trigger-pipeline, loop-prevention. No links to non-existent pages.
