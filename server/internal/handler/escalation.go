package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/service"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Escalations — the agent's out-of-band "I am stuck" hatch, and the human's
// half of it (docs/orchestration-upgrade-plan.md §B1).
//
// Two sides, two gates:
//
//   - RAISE (POST /api/issues/{id}/escalations) is AGENT-ONLY. A human with
//     a question does not need this object; they comment. Letting a human
//     raise one would also let them park someone else's run.
//   - RESOLVE / CANCEL are HUMAN-ONLY (RequireHumanActor, the same gate as
//     review-decision and qa-override). Never the agent that raised it, and
//     never an autopilot — an escalation an agent can clear itself is just a
//     slower way of guessing.
//
// Reads are open to any member who can see the issue (loadIssueForUser
// already applies the visibility gate and the workspace fence).

// escalationListLimit caps the per-issue history read. The card shows the
// open one plus recent decisions; nobody scrolls an escalation archive.
const escalationListLimit = 20

// escalationQueueLimit caps the workspace decision queue read.
const escalationQueueLimit = 200

type raiseEscalationRequest struct {
	// Need is the one-sentence ask. The CLI sends --need here.
	Need string `json:"need"`
	// Tried is the optional "what I already ruled out".
	Tried   string   `json:"tried"`
	Options []string `json:"options"`
	Kind    string   `json:"kind"`
}

type escalationResolveRequest struct {
	Answer string `json:"answer"`
}

// RaiseEscalation handles POST /api/issues/{id}/escalations.
//
// Returns 200 with the escalation. The CLI prints a sentinel and exits 0
// IMMEDIATELY — it does not poll. That is the whole difference from
// `agora telegram ask`, which blocks a runtime slot for up to an hour
// waiting on a human whose median response time is 24.9 hours.
func (h *Handler) RaiseEscalation(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))
	if actorType != "agent" {
		writeError(w, http.StatusForbidden, "only an agent working on this issue can raise an escalation")
		return
	}

	var req raiseEscalationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Need) == "" {
		writeError(w, http.StatusBadRequest, "need is required: say what you need in one sentence")
		return
	}

	// The raising agent and its in-flight task. Both come from server-set
	// identity headers (resolveActor above already refused to trust a bare
	// X-Agent-ID), so neither can be spoofed into parking someone else's run.
	var agentUUID pgtype.UUID
	if parsed, err := util.ParseUUID(actorID); err == nil {
		agentUUID = parsed
	}
	taskUUID := h.callingTaskForIssue(r, issue)

	esc, err := h.TaskService.OpenEscalation(r.Context(), service.OpenEscalationInput{
		Issue:    issue,
		TaskID:   taskUUID,
		AgentID:  agentUUID,
		Kind:     req.Kind,
		Prompt:   req.Need,
		Detail:   req.Tried,
		Options:  req.Options,
		RiskTier: h.issueRiskTier(r.Context(), issue),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to raise escalation")
		return
	}
	writeJSON(w, http.StatusOK, service.EscalationToMap(esc))
}

// ListIssueEscalations handles GET /api/issues/{id}/escalations — the read
// behind the issue-detail card. Open first, then the answered history.
func (h *Handler) ListIssueEscalations(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	limit := escalationListLimit
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v < escalationListLimit {
		limit = v
	}
	rows, err := h.Queries.ListTaskEscalationsForIssue(r.Context(), db.ListTaskEscalationsForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		Limit:       int32(limit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list escalations")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"escalations": escalationsToList(rows)})
}

// ListWorkspaceEscalations handles GET /api/escalations — every open
// escalation in the workspace, oldest first. Until the ranked decision queue
// lands (plan §A2), AGE is the ranking: the question nobody has looked at
// longest is the one costing the most.
func (h *Handler) ListWorkspaceEscalations(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace_id")
		return
	}
	rows, err := h.Queries.ListOpenTaskEscalations(r.Context(), db.ListOpenTaskEscalationsParams{
		WorkspaceID: wsUUID,
		Limit:       escalationQueueLimit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list escalations")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"escalations": escalationsToList(rows)})
}

// ResolveEscalation handles POST /api/escalations/{escalationId}/resolve.
// Route-gated by RequireHumanActor: a machine credential can never answer a
// question that exists because a machine could not answer it.
func (h *Handler) ResolveEscalation(w http.ResponseWriter, r *http.Request) {
	esc, issue, ok := h.loadEscalationForUser(w, r)
	if !ok {
		return
	}
	userIDStr, ok := requireUserID(w, r)
	if !ok {
		return
	}
	userUUID, uok := parseUUIDOrBadRequest(w, userIDStr, "user_id")
	if !uok {
		return
	}

	var req escalationResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Answer) == "" {
		writeError(w, http.StatusBadRequest, "answer is required")
		return
	}

	answered, resumed, err := h.TaskService.ResolveEscalation(r.Context(), issue, esc.ID, req.Answer, userUUID)
	if err != nil {
		if errors.Is(err, service.ErrEscalationAlreadyResolved) {
			writeError(w, http.StatusConflict, "this escalation has already been answered")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to resolve escalation")
		return
	}

	out := service.EscalationToMap(answered)
	if resumed != nil {
		out["resumed_task_id"] = uuidToString(resumed.ID)
	}
	writeJSON(w, http.StatusOK, out)
}

// CancelEscalation handles POST /api/escalations/{escalationId}/cancel — the
// human withdraws the question without answering it. The parked run is NOT
// resumed; whoever cancels owns what happens next (terminate the task, or
// re-run the issue).
func (h *Handler) CancelEscalation(w http.ResponseWriter, r *http.Request) {
	esc, issue, ok := h.loadEscalationForUser(w, r)
	if !ok {
		return
	}
	userIDStr, ok := requireUserID(w, r)
	if !ok {
		return
	}
	userUUID, uok := parseUUIDOrBadRequest(w, userIDStr, "user_id")
	if !uok {
		return
	}
	cancelled, err := h.TaskService.CancelEscalation(r.Context(), issue, esc.ID, userUUID)
	if err != nil {
		if errors.Is(err, service.ErrEscalationAlreadyResolved) {
			writeError(w, http.StatusConflict, "this escalation has already been resolved")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to cancel escalation")
		return
	}
	writeJSON(w, http.StatusOK, service.EscalationToMap(cancelled))
}

// --- helpers ----------------------------------------------------------------

// loadEscalationForUser resolves {escalationId} inside the caller's workspace
// and then re-checks the ISSUE through loadIssueForUser, so the issue
// visibility gate applies to escalations too. Two lookups, deliberately: the
// workspace fence alone would let a member answer a question on an issue they
// are not allowed to read.
func (h *Handler) loadEscalationForUser(w http.ResponseWriter, r *http.Request) (db.TaskEscalation, db.Issue, bool) {
	if _, ok := requireUserID(w, r); !ok {
		return db.TaskEscalation{}, db.Issue{}, false
	}
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return db.TaskEscalation{}, db.Issue{}, false
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace_id")
		return db.TaskEscalation{}, db.Issue{}, false
	}
	escUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "escalationId"), "escalation_id")
	if !ok {
		return db.TaskEscalation{}, db.Issue{}, false
	}
	esc, err := h.Queries.GetTaskEscalation(r.Context(), db.GetTaskEscalationParams{
		ID:          escUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "escalation not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to load escalation")
		}
		return db.TaskEscalation{}, db.Issue{}, false
	}
	issue, ok := h.loadIssueForUser(w, r, uuidToString(esc.IssueID))
	if !ok {
		return db.TaskEscalation{}, db.Issue{}, false
	}
	return esc, issue, true
}

// callingTaskForIssue returns the in-flight task the calling agent is running
// on THIS issue, or an invalid UUID when the caller is not inside a run (a
// standalone CLI invocation, say). Invalid means "nothing to park" — the
// escalation is still raised, because the question is real either way.
func (h *Handler) callingTaskForIssue(r *http.Request, issue db.Issue) pgtype.UUID {
	taskIDStr := strings.TrimSpace(r.Header.Get("X-Task-ID"))
	if taskIDStr == "" {
		return pgtype.UUID{}
	}
	taskUUID, err := util.ParseUUID(taskIDStr)
	if err != nil {
		return pgtype.UUID{}
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskUUID)
	if err != nil || !task.IssueID.Valid {
		return pgtype.UUID{}
	}
	if uuidToString(task.IssueID) != uuidToString(issue.ID) {
		// A task token bound to a different issue must never park this one.
		return pgtype.UUID{}
	}
	return task.ID
}

func escalationsToList(rows []db.TaskEscalation) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, service.EscalationToMap(row))
	}
	return out
}
