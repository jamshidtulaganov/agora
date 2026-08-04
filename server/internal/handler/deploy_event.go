package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// recentDeployEventsLimit bounds the Deploy lens's history rows — a handful
// of recent attempts is enough context (retry, rollback), not a full audit
// log surface.
const recentDeployEventsLimit = 10

// DeployEventResponse is one Tier-1 (QA-box git-sync) deploy attempt.
type DeployEventResponse struct {
	ID         string `json:"id"`
	IssueID    string `json:"issue_id"`
	Ref        string `json:"ref"`
	Target     string `json:"target"`
	Status     string `json:"status"`
	Summary    string `json:"summary"`
	CapturedAt string `json:"captured_at"`
}

func deployEventToResponse(e db.DeployEvent) DeployEventResponse {
	return DeployEventResponse{
		ID:         uuidToString(e.ID),
		IssueID:    uuidToString(e.IssueID),
		Ref:        e.Ref,
		Target:     e.Target,
		Status:     e.Status,
		Summary:    e.Summary,
		CapturedAt: e.CapturedAt.Time.Format(time.RFC3339),
	}
}

// IssueDeployEventsResponse is the Deploy lens's read: the freshest event
// (what deploySynced derives from) plus a short recent history.
type IssueDeployEventsResponse struct {
	Latest *DeployEventResponse  `json:"latest"`
	Recent []DeployEventResponse `json:"recent"`
}

// GetIssueDeployEvents returns the latest deploy_event for an issue plus a
// short recent history, or an all-null/empty body when the issue has never
// been deployed — a normal, expected response, not an error. Same membership
// check as every other issue-scoped read (loadIssueForUser).
func (h *Handler) GetIssueDeployEvents(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	resp := IssueDeployEventsResponse{Recent: []DeployEventResponse{}}

	latest, err := h.Queries.GetLatestDeployEventForIssue(r.Context(), db.GetLatestDeployEventForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("get latest deploy event failed", "error", err, "issue_id", uuidToString(issue.ID))
		writeError(w, http.StatusInternalServerError, "failed to load deploy events")
		return
	}
	if err == nil {
		latestResp := deployEventToResponse(latest)
		resp.Latest = &latestResp
	}

	recent, err := h.Queries.ListDeployEventsForIssue(r.Context(), db.ListDeployEventsForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		Limit:       recentDeployEventsLimit,
	})
	if err != nil {
		slog.Warn("list deploy events failed", "error", err, "issue_id", uuidToString(issue.ID))
		writeError(w, http.StatusInternalServerError, "failed to load deploy events")
		return
	}
	for _, e := range recent {
		resp.Recent = append(resp.Recent, deployEventToResponse(e))
	}

	writeJSON(w, http.StatusOK, resp)
}
