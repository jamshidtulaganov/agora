package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Dev-runtime pinning (daemon-per-dev phase 1, see
// docs/daemon-per-dev-affinity-design.md): when a QA-squad agent is put on an
// issue whose developer runs the project's app on their OWN daemon, the task
// executes THERE — an agent on that machine is the only thing that can reach
// the app's 127.0.0.1. Routing rides the existing enqueue-time
// agent_task_queue.runtime_id channel (candidates + claims are already
// per-runtime), so no schema change: the pin is a runtime_id override plus a
// context marker the watchdog uses for offline fallback.

// maybePinTaskToDevRuntime re-routes a just-enqueued, still-queued task onto
// the issue-developer's own runtime when every gate passes:
//
//	labs.qa_dev_runtimes on → mentioned agent is QA-squad → the issue has a
//	human developer → that dev has an ONLINE runtime declaring a local app
//	for the issue's project (metadata.dev_apps[project_id]).
//
// Best-effort: any miss leaves the task exactly where the normal enqueue put
// it. Returns the (possibly updated) task.
func (s *TaskService) maybePinTaskToDevRuntime(ctx context.Context, issue db.Issue, agent db.Agent, task db.AgentTaskQueue) db.AgentTaskQueue {
	if !issue.ProjectID.Valid {
		return task
	}
	ws, err := s.Queries.GetWorkspace(ctx, issue.WorkspaceID)
	if err != nil || !util.ParseWorkspaceLabs(ws.Settings).QADevRuntimes {
		return task
	}
	if inQA, qerr := s.Queries.AgentInQASquad(ctx, db.AgentInQASquadParams{
		WorkspaceID: issue.WorkspaceID, MemberID: agent.ID,
	}); qerr != nil || !inQA {
		return task
	}
	devUser, ok := s.developerUserForIssue(ctx, issue)
	if !ok {
		return task
	}
	runtime, err := s.Queries.GetDevRuntimeForProject(ctx, db.GetDevRuntimeForProjectParams{
		WorkspaceID: issue.WorkspaceID,
		OwnerID:     devUser,
		ProjectID:   util.UUIDToString(issue.ProjectID),
	})
	if err != nil {
		// Fallback: a local_directory resource binds this project to a folder
		// on a specific daemon — that daemon IS the developer's machine, so
		// pin QA there too. Unlike dev_apps (a per-dev URL declaration) this is
		// an explicit project→daemon binding, so it is NOT gated on the
		// runtime being owned by the issue's developer; the labs +
		// AgentInQASquad gates above still apply.
		ldRuntime, lok := s.localDirectoryRuntimeForProject(ctx, issue)
		if !lok {
			return task // no online dev runtime and no online local_directory — normal flow
		}
		runtime = ldRuntime
	}
	if task.RuntimeID.Valid && task.RuntimeID.Bytes == runtime.ID.Bytes {
		return task // already routed there (agent lives on the dev's runtime)
	}

	pinCtx, _ := json.Marshal(map[string]string{
		"dev_runtime_pin":  "true",
		"dev_runtime_home": util.UUIDToString(task.RuntimeID),
	})
	pinned, err := s.Queries.PinTaskToDevRuntime(ctx, db.PinTaskToDevRuntimeParams{
		ID:         task.ID,
		RuntimeID:  runtime.ID,
		WaitReason: pgtype.Text{String: "waiting_dev_runtime:" + runtime.Name, Valid: true},
		PinContext: pinCtx,
	})
	if err != nil {
		slog.Warn("dev-runtime pin failed; task stays on home runtime",
			"task_id", util.UUIDToString(task.ID), "error", err)
		return task
	}
	slog.Info("task pinned to dev runtime",
		"task_id", util.UUIDToString(pinned.ID),
		"runtime_id", util.UUIDToString(runtime.ID),
		"runtime_name", runtime.Name,
		"issue_id", util.UUIDToString(issue.ID))
	// The pin moved the task between runtimes' claim queues — wake the target.
	s.NotifyTaskEnqueued(ctx, pinned)
	return pinned
}

// localDirectoryRuntimeForProject resolves the ONLINE runtime hosting a
// local_directory resource on the issue's project. It mirrors the handler-side
// localDirectoryQATarget (service must not import handler), returning the
// runtime so the QA task can be pinned to the machine where the folder — and
// thus the app under test — lives. The first online local_directory daemon
// wins; offline daemons are skipped.
func (s *TaskService) localDirectoryRuntimeForProject(ctx context.Context, issue db.Issue) (db.AgentRuntime, bool) {
	if !issue.ProjectID.Valid {
		return db.AgentRuntime{}, false
	}
	rows, err := s.Queries.ListProjectResources(ctx, issue.ProjectID)
	if err != nil {
		return db.AgentRuntime{}, false
	}
	for _, res := range rows {
		if res.ResourceType != "local_directory" {
			continue
		}
		var ref struct {
			DaemonID string `json:"daemon_id"`
		}
		if err := json.Unmarshal(res.ResourceRef, &ref); err != nil || ref.DaemonID == "" {
			continue
		}
		rt, err := s.Queries.GetOnlineRuntimeForDaemon(ctx, db.GetOnlineRuntimeForDaemonParams{
			WorkspaceID: issue.WorkspaceID,
			DaemonID:    pgtype.Text{String: ref.DaemonID, Valid: true},
		})
		if err != nil {
			continue
		}
		return rt, true
	}
	return db.AgentRuntime{}, false
}

// developerUserForIssue resolves the human developer (user id) behind an
// issue: a member assignee IS the developer; an agent assignee maps to its
// owner. Mirrors the handler-side resolver used for QA box routing.
func (s *TaskService) developerUserForIssue(ctx context.Context, issue db.Issue) (pgtype.UUID, bool) {
	if !issue.AssigneeType.Valid || !issue.AssigneeID.Valid {
		return pgtype.UUID{}, false
	}
	switch issue.AssigneeType.String {
	case "agent":
		agent, err := s.Queries.GetAgent(ctx, issue.AssigneeID)
		if err == nil && agent.OwnerID.Valid {
			return agent.OwnerID, true
		}
	case "member":
		member, err := s.Queries.GetMember(ctx, issue.AssigneeID)
		if err == nil && member.WorkspaceID.Bytes == issue.WorkspaceID.Bytes {
			return member.UserID, true
		}
	}
	return pgtype.UUID{}, false
}

// SweepStaleDevPinnedTasks is the watchdog arm of dev-runtime pinning: pinned
// tasks whose runtime went offline (or that waited past maxWaitSecs) either
// fall back to their home runtime (soft, default) or stay pinned loudly
// (labs.qa_dev_runtimes_strict). Returns how many tasks were unpinned.
func (s *TaskService) SweepStaleDevPinnedTasks(ctx context.Context, maxWaitSecs float64) int {
	tasks, err := s.Queries.ListStaleDevPinnedQueuedTasks(ctx, maxWaitSecs)
	if err != nil {
		slog.Warn("dev-pin sweep: list failed", "error", err)
		return 0
	}
	unpinned := 0
	for _, task := range tasks {
		issue, ierr := s.Queries.GetIssue(ctx, task.IssueID)
		if ierr != nil {
			continue
		}
		ws, werr := s.Queries.GetWorkspace(ctx, issue.WorkspaceID)
		if werr != nil {
			continue
		}
		if util.ParseWorkspaceLabs(ws.Settings).QADevRuntimesStrict {
			// Strict: never reroute. Escalate once per task (marker in context
			// would need another write; the wait_reason already renders in the
			// UI, so a log line is enough here — the QA watchdog's qa:stale
			// escalation covers the issue-level noise).
			slog.Info("dev-pinned task waiting (strict mode)",
				"task_id", util.UUIDToString(task.ID), "wait_reason", task.WaitReason.String)
			continue
		}
		home := s.devPinHomeRuntime(ctx, task)
		if !home.Valid {
			continue
		}
		moved, uerr := s.Queries.UnpinTaskToRuntime(ctx, db.UnpinTaskToRuntimeParams{
			ID: task.ID, RuntimeID: home,
		})
		if uerr != nil {
			slog.Warn("dev-pin fallback failed", "task_id", util.UUIDToString(task.ID), "error", uerr)
			continue
		}
		unpinned++
		if task.IssueID.Valid {
			s.createAgentComment(ctx, task.IssueID, task.AgentID,
				"↪️ The developer's daemon is offline — this QA run fell back to the shared runtime. "+
					"QA target resolution reverts to the project's box/smoke URL for this run.",
				"system", pgtype.UUID{})
		}
		slog.Info("dev-pinned task fell back to home runtime",
			"task_id", util.UUIDToString(moved.ID), "runtime_id", util.UUIDToString(home))
		s.NotifyTaskEnqueued(ctx, moved)
	}
	return unpinned
}

// devPinHomeRuntime recovers the runtime a pinned task should fall back to:
// the recorded home from the pin marker, else the agent's current runtime.
func (s *TaskService) devPinHomeRuntime(ctx context.Context, task db.AgentTaskQueue) pgtype.UUID {
	var c struct {
		Home string `json:"dev_runtime_home"`
	}
	if len(task.Context) > 0 && json.Unmarshal(task.Context, &c) == nil && c.Home != "" {
		if id, err := util.ParseUUID(c.Home); err == nil {
			return id
		}
	}
	agent, err := s.Queries.GetAgent(ctx, task.AgentID)
	if err != nil || !agent.RuntimeID.Valid {
		return pgtype.UUID{}
	}
	return agent.RuntimeID
}
