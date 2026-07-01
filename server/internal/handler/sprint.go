package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type SprintResponse struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	ProjectID   string  `json:"project_id"`
	Name        string  `json:"name"`
	Goal        string  `json:"goal"`
	Status      string  `json:"status"`
	StartDate   *string `json:"start_date"`
	EndDate     *string `json:"end_date"`
	// Branch is the sprint's OWN dedicated integration branch — cut off the prod
	// branch, worked by the sprint's agents, and merged back to prod by a human
	// at sprint end. It must NOT be a protected/prod branch (see
	// sprintBranchRejected). Empty = the code falls back to the sprint/<id>
	// convention.
	Branch    string `json:"branch"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// protectedSprintBranches is the set of branch names a sprint may NOT use as
// its integration branch — the production / long-lived branches sprint work
// must never target directly (agents would commit onto, and QA would deploy,
// the prod branch). Sprints get their OWN dedicated branch cut off the prod
// branch; a human merges that sprint branch back into prod at sprint end.
// Configured via AGORA_PROTECTED_SPRINT_BRANCHES (comma-separated, e.g.
// "billing"); the common prod names are always included as a backstop.
func protectedSprintBranches() map[string]bool {
	set := map[string]bool{"master": true, "main": true, "production": true, "prod": true}
	for _, b := range strings.Split(os.Getenv("AGORA_PROTECTED_SPRINT_BRANCHES"), ",") {
		if b = strings.ToLower(strings.TrimSpace(b)); b != "" {
			set[b] = true
		}
	}
	return set
}

// sprintBranchRejected reports whether `branch` is a protected prod branch that
// must not be set as a sprint's integration branch. Case-insensitive; empty is
// allowed (falls back to the sprint/<id> convention).
func sprintBranchRejected(branch string) bool {
	b := strings.ToLower(strings.TrimSpace(branch))
	if b == "" {
		return false
	}
	return protectedSprintBranches()[b]
}

func sprintToResponse(s db.Sprint) SprintResponse {
	return SprintResponse{
		ID:          uuidToString(s.ID),
		WorkspaceID: uuidToString(s.WorkspaceID),
		ProjectID:   uuidToString(s.ProjectID),
		Name:        s.Name,
		Goal:        s.Goal,
		Status:      s.Status,
		StartDate:   timestampToPtr(s.StartDate),
		EndDate:     timestampToPtr(s.EndDate),
		Branch:      s.Branch,
		CreatedAt:   timestampToString(s.CreatedAt),
		UpdatedAt:   timestampToString(s.UpdatedAt),
	}
}

type CreateSprintRequest struct {
	Name      string  `json:"name"`
	Goal      *string `json:"goal"`
	Status    string  `json:"status"`
	StartDate *string `json:"start_date"`
	EndDate   *string `json:"end_date"`
	Branch    *string `json:"branch"`
}

type UpdateSprintRequest struct {
	Name      *string `json:"name"`
	Goal      *string `json:"goal"`
	Status    *string `json:"status"`
	StartDate *string `json:"start_date"`
	EndDate   *string `json:"end_date"`
	Branch    *string `json:"branch"`
}

type SetIssueSprintRequest struct {
	SprintID string `json:"sprint_id"`
}

// validSprintStatuses mirrors the CHECK constraint on the sprint table
// (migration 122). CreateSprint / UpdateSprint pre-validate against these so an
// unknown enum value returns a clean 400 with the allowed list instead of
// surfacing the DB CHECK violation as a 500 (mirrors validProjectStatuses).
var validSprintStatuses = []string{"planned", "active", "completed"}

func validateSprintStatus(w http.ResponseWriter, value string) bool {
	for _, a := range validSprintStatuses {
		if value == a {
			return true
		}
	}
	writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid status %q; valid values: %s", value, strings.Join(validSprintStatuses, ", ")))
	return false
}

// parseSprintDate parses an optional RFC3339 timestamp from a request body.
// A nil pointer yields a NULL timestamptz; an invalid string returns an error
// suitable for a 400 response.
func parseSprintDate(raw *string, field string) (pgtype.Timestamptz, error) {
	if raw == nil {
		return pgtype.Timestamptz{}, nil
	}
	s := strings.TrimSpace(*raw)
	if s == "" {
		return pgtype.Timestamptz{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		if t, err = time.Parse(time.RFC3339, s); err != nil {
			// The sprint UI (create/edit modals) serializes calendar dates as
			// date-only "YYYY-MM-DD" (toDateOnly) — accept that too, parsed at
			// UTC midnight. Without this the modal save 400s.
			if t, err = time.Parse("2006-01-02", s); err != nil {
				return pgtype.Timestamptz{}, fmt.Errorf("invalid %s; expected an RFC3339 timestamp or a YYYY-MM-DD date", field)
			}
		}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}, nil
}

// ---------------------------------------------------------------------------
// Handlers — sprint CRUD
// ---------------------------------------------------------------------------

// ListSprints returns the sprints for a project, newest first. The project is
// resolved within the caller's workspace so a foreign project id 404s rather
// than leaking another tenant's sprints.
func (h *Handler) ListSprints(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	workspaceID := h.resolveWorkspaceID(r)
	projUUID, ok := parseUUIDOrBadRequest(w, projectID, "project id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	// Authorize via the project — if it's not in this workspace, the caller
	// shouldn't see its sprints.
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID: projUUID, WorkspaceID: wsUUID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		slog.Warn("ListSprints GetProjectInWorkspace failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list sprints")
		return
	}
	sprints, err := h.Queries.ListSprintsByProject(r.Context(), projUUID)
	if err != nil {
		slog.Warn("ListSprints failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list sprints")
		return
	}
	resp := make([]SprintResponse, len(sprints))
	for i, s := range sprints {
		resp[i] = sprintToResponse(s)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sprints": resp, "total": len(resp)})
}

func (h *Handler) CreateSprint(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	var req CreateSprintRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	projUUID, ok := parseUUIDOrBadRequest(w, projectID, "project id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	// The project must belong to this workspace; this also fixes the sprint's
	// workspace_id to the project's workspace.
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID: projUUID, WorkspaceID: wsUUID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		slog.Warn("CreateSprint GetProjectInWorkspace failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to create sprint")
		return
	}
	status := req.Status
	if status == "" {
		status = "planned"
	}
	if !validateSprintStatus(w, status) {
		return
	}
	goal := ""
	if req.Goal != nil {
		goal = *req.Goal
	}
	startDate, err := parseSprintDate(req.StartDate, "start_date")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	endDate, err := parseSprintDate(req.EndDate, "end_date")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	branch := ""
	if req.Branch != nil {
		branch = strings.TrimSpace(*req.Branch)
	}
	if sprintBranchRejected(branch) {
		writeError(w, http.StatusBadRequest, "sprint branch cannot be a protected/production branch ("+branch+") — cut a dedicated sprint branch off it and set that instead")
		return
	}
	sprint, err := h.Queries.CreateSprint(r.Context(), db.CreateSprintParams{
		WorkspaceID: wsUUID,
		ProjectID:   projUUID,
		Name:        name,
		Goal:        goal,
		Status:      status,
		StartDate:   startDate,
		EndDate:     endDate,
		Branch:      branch,
	})
	if err != nil {
		if isCheckViolation(err) {
			writeError(w, http.StatusBadRequest, "sprint create rejected: a field value failed a database constraint")
			return
		}
		slog.Error("CreateSprint failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to create sprint")
		return
	}
	resp := sprintToResponse(sprint)
	h.publish(protocol.EventSprintCreated, workspaceID, "member", userID, map[string]any{"sprint": resp})
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) GetSprintByID(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	workspaceID := h.resolveWorkspaceID(r)
	idUUID, ok := parseUUIDOrBadRequest(w, id, "sprint id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	sprint, err := h.Queries.GetSprint(r.Context(), db.GetSprintParams{
		ID: idUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sprint not found")
			return
		}
		slog.Warn("GetSprint failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to get sprint")
		return
	}
	writeJSON(w, http.StatusOK, sprintToResponse(sprint))
}

func (h *Handler) UpdateSprint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	workspaceID := h.resolveWorkspaceID(r)
	idUUID, ok := parseUUIDOrBadRequest(w, id, "sprint id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	prev, err := h.Queries.GetSprint(r.Context(), db.GetSprintParams{
		ID: idUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sprint not found")
			return
		}
		slog.Warn("UpdateSprint GetSprint failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to update sprint")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	var req UpdateSprintRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Distinguish "field omitted" from "field set to null" so a PUT can clear
	// start_date / end_date back to NULL without wiping unrelated columns —
	// mirrors UpdateProject's rawFields pattern.
	var rawFields map[string]json.RawMessage
	json.Unmarshal(bodyBytes, &rawFields)

	params := db.UpdateSprintParams{
		ID:          prev.ID,
		WorkspaceID: wsUUID,
		Name:        prev.Name,
		Goal:        prev.Goal,
		Status:      prev.Status,
		StartDate:   prev.StartDate,
		EndDate:     prev.EndDate,
		Branch:      prev.Branch,
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "name is required")
			return
		}
		params.Name = name
	}
	if req.Goal != nil {
		params.Goal = *req.Goal
	}
	if req.Status != nil {
		if !validateSprintStatus(w, *req.Status) {
			return
		}
		params.Status = *req.Status
	}
	if _, ok := rawFields["start_date"]; ok {
		startDate, err := parseSprintDate(req.StartDate, "start_date")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		params.StartDate = startDate
	}
	if _, ok := rawFields["end_date"]; ok {
		endDate, err := parseSprintDate(req.EndDate, "end_date")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		params.EndDate = endDate
	}
	if req.Branch != nil {
		params.Branch = strings.TrimSpace(*req.Branch)
		if sprintBranchRejected(params.Branch) {
			writeError(w, http.StatusBadRequest, "sprint branch cannot be a protected/production branch ("+params.Branch+") — cut a dedicated sprint branch off it and set that instead")
			return
		}
	}

	sprint, err := h.Queries.UpdateSprint(r.Context(), params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sprint not found")
			return
		}
		if isCheckViolation(err) {
			writeError(w, http.StatusBadRequest, "sprint update rejected: a field value failed a database constraint")
			return
		}
		slog.Error("UpdateSprint failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to update sprint")
		return
	}
	resp := sprintToResponse(sprint)
	h.publish(protocol.EventSprintUpdated, workspaceID, "member", userID, map[string]any{"sprint": resp})
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) DeleteSprint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	workspaceID := h.resolveWorkspaceID(r)
	idUUID, ok := parseUUIDOrBadRequest(w, id, "sprint id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	// Confirm the sprint is in this workspace before deleting so a foreign id
	// returns 404 rather than a silent no-op 204 (mirror of DeleteProject's
	// precheck via GetProjectInWorkspace).
	sprint, err := h.Queries.GetSprint(r.Context(), db.GetSprintParams{
		ID: idUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sprint not found")
			return
		}
		slog.Warn("DeleteSprint GetSprint failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to delete sprint")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if err := h.Queries.DeleteSprint(r.Context(), db.DeleteSprintParams{
		ID: sprint.ID, WorkspaceID: sprint.WorkspaceID,
	}); err != nil {
		slog.Error("DeleteSprint failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to delete sprint")
		return
	}
	h.publish(protocol.EventSprintDeleted, workspaceID, "member", userID, map[string]any{"sprint_id": uuidToString(sprint.ID)})
	w.WriteHeader(http.StatusNoContent)
}

// ListSprintIssues returns the issues assigned to a sprint.
func (h *Handler) ListSprintIssues(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	workspaceID := h.resolveWorkspaceID(r)
	idUUID, ok := parseUUIDOrBadRequest(w, id, "sprint id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	// Authorize via the sprint — if it's not in this workspace, the caller
	// shouldn't see its issues.
	sprint, err := h.Queries.GetSprint(r.Context(), db.GetSprintParams{
		ID: idUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sprint not found")
			return
		}
		slog.Warn("ListSprintIssues GetSprint failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list sprint issues")
		return
	}
	issues, err := h.Queries.ListIssuesBySprint(r.Context(), sprint.ID)
	if err != nil {
		slog.Warn("ListIssuesBySprint failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list sprint issues")
		return
	}
	prefix := h.getIssuePrefix(r.Context(), sprint.WorkspaceID)
	resp := make([]IssueResponse, len(issues))
	for i, issue := range issues {
		resp[i] = issueToResponse(issue, prefix)
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": resp})
}

// ---------------------------------------------------------------------------
// Handlers — issue↔sprint assign/unassign
// ---------------------------------------------------------------------------

// SetIssueSprint assigns an issue to a sprint (upsert — an issue belongs to at
// most one sprint). Both the issue and the sprint must belong to the same
// workspace.
func (h *Handler) SetIssueSprint(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req SetIssueSprintRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SprintID == "" {
		writeError(w, http.StatusBadRequest, "sprint_id is required")
		return
	}

	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}
	sprintUUID, ok := parseUUIDOrBadRequest(w, req.SprintID, "sprint_id")
	if !ok {
		return
	}
	// The sprint must belong to the issue's workspace; GetSprint is workspace-
	// scoped so a foreign sprint id 404s instead of cross-linking tenants.
	sprint, err := h.Queries.GetSprint(r.Context(), db.GetSprintParams{
		ID: sprintUUID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sprint not found")
			return
		}
		slog.Warn("SetIssueSprint GetSprint failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to set sprint")
		return
	}

	if err := h.Queries.SetIssueSprint(r.Context(), db.SetIssueSprintParams{
		IssueID:  issue.ID,
		SprintID: sprint.ID,
	}); err != nil {
		slog.Warn("SetIssueSprint failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to set sprint")
		return
	}
	resp := sprintToResponse(sprint)
	h.publish(protocol.EventIssueSprintChanged, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{
		"issue_id": uuidToString(issue.ID),
		"sprint":   resp,
	})
	writeJSON(w, http.StatusOK, map[string]any{"sprint": resp})
}

// GetIssueSprint returns the sprint an issue is assigned to, or {"sprint": null}
// when it is in none. Lets the UI fetch an issue's sprint directly instead of
// scanning every sprint's issue list (the join already scopes by issue_id, and
// loadIssueForUser is the workspace gate). GET /api/issues/{id}/sprint.
func (h *Handler) GetIssueSprint(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}
	sprint, err := h.Queries.GetSprintForIssue(r.Context(), issue.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, map[string]any{"sprint": nil})
			return
		}
		slog.Warn("GetIssueSprint failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to load sprint")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sprint": sprintToResponse(sprint)})
}

// RemoveIssueSprint unassigns an issue from whatever sprint it's in.
func (h *Handler) RemoveIssueSprint(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}
	if err := h.Queries.RemoveIssueSprint(r.Context(), issue.ID); err != nil {
		slog.Warn("RemoveIssueSprint failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to remove sprint")
		return
	}
	h.publish(protocol.EventIssueSprintChanged, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{
		"issue_id": uuidToString(issue.ID),
		"sprint":   nil,
	})
	w.WriteHeader(http.StatusNoContent)
}
