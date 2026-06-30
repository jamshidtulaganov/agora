package handler

import (
	"net/http"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Policy Agent — the fleet watchdog. A workspace-scoped read of agent SPEED +
// health over agent_task_queue: per-agent run-duration / queue-wait, stalled
// runs (stuck in 'running'), recent failures (with the classifier), and looping
// issues (one issue churning many tasks). Monitor + surface only here; acting on
// a stalled/looping task (auto-cancel) is a gated follow-up.

const (
	policyStallMinutes  = 20 // a run stuck this long is treated as stalled
	policyLoopThreshold = 4  // this many tasks for one issue in an hour = a loop
)

type policyAgentSpeed struct {
	AgentID         string  `json:"agent_id"`
	AgentName       string  `json:"agent_name"`
	TaskCount       int64   `json:"task_count"`
	FailedCount     int64   `json:"failed_count"`
	AvgRunSeconds   float64 `json:"avg_run_seconds"`
	P95RunSeconds   float64 `json:"p95_run_seconds"`
	AvgQueueSeconds float64 `json:"avg_queue_seconds"`
}

type policyStalledTask struct {
	TaskID    string  `json:"task_id"`
	AgentID   string  `json:"agent_id"`
	AgentName string  `json:"agent_name"`
	IssueID   string  `json:"issue_id"`
	StartedAt *string `json:"started_at"`
	Attempt   int32   `json:"attempt"`
}

type policyFailedTask struct {
	TaskID        string  `json:"task_id"`
	AgentID       string  `json:"agent_id"`
	AgentName     string  `json:"agent_name"`
	IssueID       string  `json:"issue_id"`
	FailureReason string  `json:"failure_reason"`
	Error         string  `json:"error"`
	StartedAt     *string `json:"started_at"`
	CompletedAt   *string `json:"completed_at"`
	Attempt       int32   `json:"attempt"`
}

type policyLoopingIssue struct {
	IssueID    string  `json:"issue_id"`
	TaskCount  int64   `json:"task_count"`
	LastTaskAt *string `json:"last_task_at"`
}

type policyFleetHealthResponse struct {
	StallMinutes   int                  `json:"stall_minutes"`
	LoopThreshold  int                  `json:"loop_threshold"`
	Agents         []policyAgentSpeed   `json:"agents"`
	Stalled        []policyStalledTask  `json:"stalled"`
	RecentFailures []policyFailedTask   `json:"recent_failures"`
	Looping        []policyLoopingIssue `json:"looping"`
}

// GetPolicyFleetHealth returns the workspace's agent-fleet speed + health.
// GET /api/policy/fleet-health.
func (h *Handler) GetPolicyFleetHealth(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace_id")
	if !ok {
		return
	}
	ctx := r.Context()

	resp := policyFleetHealthResponse{
		StallMinutes:   policyStallMinutes,
		LoopThreshold:  policyLoopThreshold,
		Agents:         []policyAgentSpeed{},
		Stalled:        []policyStalledTask{},
		RecentFailures: []policyFailedTask{},
		Looping:        []policyLoopingIssue{},
	}

	if rows, err := h.Queries.GetAgentSpeedMetrics(ctx, wsUUID); err == nil {
		for _, m := range rows {
			resp.Agents = append(resp.Agents, policyAgentSpeed{
				AgentID:         uuidToString(m.AgentID),
				AgentName:       m.AgentName,
				TaskCount:       m.TaskCount,
				FailedCount:     m.FailedCount,
				AvgRunSeconds:   m.AvgRunSeconds,
				P95RunSeconds:   m.P95RunSeconds,
				AvgQueueSeconds: m.AvgQueueSeconds,
			})
		}
	}

	if rows, err := h.Queries.ListStalledTasks(ctx, db.ListStalledTasksParams{
		WorkspaceID: wsUUID, StallMinutes: policyStallMinutes,
	}); err == nil {
		for _, s := range rows {
			resp.Stalled = append(resp.Stalled, policyStalledTask{
				TaskID:    uuidToString(s.ID),
				AgentID:   uuidToString(s.AgentID),
				AgentName: s.AgentName,
				IssueID:   uuidToString(s.IssueID),
				StartedAt: timestampToPtr(s.StartedAt),
				Attempt:   s.Attempt,
			})
		}
	}

	if rows, err := h.Queries.ListRecentFailedTasks(ctx, wsUUID); err == nil {
		for _, f := range rows {
			resp.RecentFailures = append(resp.RecentFailures, policyFailedTask{
				TaskID:        uuidToString(f.ID),
				AgentID:       uuidToString(f.AgentID),
				AgentName:     f.AgentName,
				IssueID:       uuidToString(f.IssueID),
				FailureReason: f.FailureReason.String,
				Error:         f.Error.String,
				StartedAt:     timestampToPtr(f.StartedAt),
				CompletedAt:   timestampToPtr(f.CompletedAt),
				Attempt:       f.Attempt,
			})
		}
	}

	if rows, err := h.Queries.ListLoopingIssues(ctx, db.ListLoopingIssuesParams{
		WorkspaceID: wsUUID, LoopThreshold: policyLoopThreshold,
	}); err == nil {
		for _, l := range rows {
			resp.Looping = append(resp.Looping, policyLoopingIssue{
				IssueID:    uuidToString(l.IssueID),
				TaskCount:  l.TaskCount,
				LastTaskAt: timestampToPtr(l.LastTaskAt),
			})
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
