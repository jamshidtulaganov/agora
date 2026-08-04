package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/service"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Knowledge-item review API — the human half of the KB flywheel. Agents
// propose items via the fenced ```knowledge-items``` comment block
// (service.CaptureKnowledgeItems); humans list, add, edit, approve
// (status → active), and retire (status → archived) them here. All three
// mutating routes are RequireHumanActor-gated at the router — including
// POST: agent task-tokens and PATs authenticate through the same
// RequireWorkspaceMember group, and an agent-minted immediately-active item
// would void the prompt-injection review gate entirely.

var knowledgeItemStatuses = map[string]bool{
	"active":   true,
	"proposed": true,
	"archived": true,
}

type knowledgeItemResponse struct {
	ID              string `json:"id"`
	ProjectID       string `json:"project_id"`
	KbName          string `json:"kb_name"`
	Module          string `json:"module"`
	Kind            string `json:"kind"`
	Title           string `json:"title"`
	Body            string `json:"body"`
	Status          string `json:"status"`
	Hits            int32  `json:"hits"`
	SourceIssueID   string `json:"source_issue_id,omitempty"`
	CreatedByType   string `json:"created_by_type"`
	CreatedByID     string `json:"created_by_id,omitempty"`
	LastConfirmedAt string `json:"last_confirmed_at,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

func knowledgeItemToResponse(it db.KnowledgeItem) knowledgeItemResponse {
	return knowledgeItemResponse{
		ID:              uuidToString(it.ID),
		ProjectID:       uuidToString(it.ProjectID),
		KbName:          it.KbName,
		Module:          it.Module,
		Kind:            it.Kind,
		Title:           it.Title,
		Body:            it.Body,
		Status:          it.Status,
		Hits:            it.Hits,
		SourceIssueID:   uuidToString(it.SourceIssueID),
		CreatedByType:   it.CreatedByType,
		CreatedByID:     uuidToString(it.CreatedByID),
		LastConfirmedAt: timestampToString(it.LastConfirmedAt),
		CreatedAt:       timestampToString(it.CreatedAt),
		UpdatedAt:       timestampToString(it.UpdatedAt),
	}
}

// loadProjectInWorkspace resolves the {id} path param to a project and 404s
// unless it belongs to the request's workspace. GetProject is NOT
// workspace-scoped; without this check a member of workspace A could pass
// B's project id and mint knowledge rows with workspace_id=A,
// project_id=B — the compile would then bake B's title and kb_skill into a
// skill in A (cross-tenant leak). Cross-workspace probes are
// indistinguishable from not-found.
func (h *Handler) loadProjectInWorkspace(w http.ResponseWriter, r *http.Request, wsUUID pgtype.UUID) (db.Project, bool) {
	projectUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return db.Project{}, false
	}
	project, err := h.Queries.GetProject(r.Context(), projectUUID)
	if err != nil || project.WorkspaceID != wsUUID {
		writeError(w, http.StatusNotFound, "project not found")
		return db.Project{}, false
	}
	return project, true
}

// ListKnowledgeItems returns a project's knowledge items. `?status=` filters
// to one status; without it, archived items are excluded.
func (h *Handler) ListKnowledgeItems(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	project, ok := h.loadProjectInWorkspace(w, r, wsUUID)
	if !ok {
		return
	}
	var statusFilter pgtype.Text
	if status := strings.TrimSpace(r.URL.Query().Get("status")); status != "" {
		if !knowledgeItemStatuses[status] {
			writeError(w, http.StatusBadRequest, "status must be one of active, proposed, archived")
			return
		}
		statusFilter = pgtype.Text{String: status, Valid: true}
	}
	rows, err := h.Queries.ListKnowledgeItemsByProject(r.Context(), db.ListKnowledgeItemsByProjectParams{
		WorkspaceID: wsUUID,
		ProjectID:   project.ID,
		Status:      statusFilter,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list knowledge items")
		return
	}
	resp := make([]knowledgeItemResponse, len(rows))
	for i, it := range rows {
		resp[i] = knowledgeItemToResponse(it)
	}
	writeJSON(w, http.StatusOK, resp)
}

type createKnowledgeItemRequest struct {
	Kind   string `json:"kind"`
	Module string `json:"module"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

// CreateKnowledgeItem adds a human-authored item, immediately active (humans
// are trusted proposers). An exact normalized-title collision with a live
// item confirms it (hits+1) instead of duplicating — same upsert the trusted
// synthesizer path uses.
func (h *Handler) CreateKnowledgeItem(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	creatorUUID, ok := parseUUIDOrBadRequest(w, requestUserID(r), "user id")
	if !ok {
		return
	}
	project, ok := h.loadProjectInWorkspace(w, r, wsUUID)
	if !ok {
		return
	}
	var req createKnowledgeItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	title := service.SanitizeKnowledgeTitle(req.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	normTitle := service.NormalizeKnowledgeTitle(title)
	if normTitle == "" {
		writeError(w, http.StatusBadRequest, "title must contain letters or digits")
		return
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind == "" {
		kind = "gotcha"
	}
	if !service.IsKnowledgeKind(kind) {
		writeError(w, http.StatusBadRequest, "kind must be one of architecture, gotcha, convention, nav, decision")
		return
	}
	kbName := service.ProjectKBSkillName(project)
	if kbName == "" {
		writeError(w, http.StatusBadRequest, "project has no resolvable knowledge base skill name")
		return
	}
	row, err := h.Queries.UpsertKnowledgeItem(r.Context(), db.UpsertKnowledgeItemParams{
		WorkspaceID:   wsUUID,
		ProjectID:     project.ID,
		KbName:        kbName,
		Module:        service.SanitizeKnowledgeModule(req.Module),
		Kind:          kind,
		Title:         title,
		Body:          service.SanitizeKnowledgeBody(req.Body),
		NormTitle:     normTitle,
		CreatedByType: "member",
		CreatedByID:   creatorUUID,
		Status:        "active",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create knowledge item")
		return
	}
	h.TaskService.RecompileKB(r.Context(), wsUUID, kbName)
	code := http.StatusOK
	if row.Inserted {
		code = http.StatusCreated
	}
	writeJSON(w, code, knowledgeItemToResponse(db.KnowledgeItem{
		ID:              row.ID,
		WorkspaceID:     row.WorkspaceID,
		ProjectID:       row.ProjectID,
		KbName:          row.KbName,
		Module:          row.Module,
		Kind:            row.Kind,
		Title:           row.Title,
		Body:            row.Body,
		NormTitle:       row.NormTitle,
		SourceIssueID:   row.SourceIssueID,
		CreatedByType:   row.CreatedByType,
		CreatedByID:     row.CreatedByID,
		Status:          row.Status,
		Hits:            row.Hits,
		LastConfirmedAt: row.LastConfirmedAt,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}))
}

type updateKnowledgeItemRequest struct {
	Module *string `json:"module"`
	Kind   *string `json:"kind"`
	Title  *string `json:"title"`
	Body   *string `json:"body"`
	Status *string `json:"status"`
}

// UpdateKnowledgeItem is the pointer-field patch endpoint: edit fields,
// approve (`{"status":"active"}`) or reject/retire (`{"status":"archived"}`).
// A title change recomputes norm_title in the same call so the dedupe key
// stays honest. Activating an item whose live twin already exists violates
// the partial unique index → 409.
func (h *Handler) UpdateKnowledgeItem(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	itemUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "itemId"), "knowledge item id")
	if !ok {
		return
	}
	item, err := h.Queries.GetKnowledgeItem(r.Context(), db.GetKnowledgeItemParams{ID: itemUUID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "knowledge item not found")
		return
	}
	var req updateKnowledgeItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	params := db.UpdateKnowledgeItemParams{ID: item.ID, WorkspaceID: wsUUID}
	if req.Kind != nil {
		kind := strings.ToLower(strings.TrimSpace(*req.Kind))
		if !service.IsKnowledgeKind(kind) {
			writeError(w, http.StatusBadRequest, "kind must be one of architecture, gotcha, convention, nav, decision")
			return
		}
		params.Kind = pgtype.Text{String: kind, Valid: true}
	}
	if req.Status != nil {
		if !knowledgeItemStatuses[*req.Status] {
			writeError(w, http.StatusBadRequest, "status must be one of active, proposed, archived")
			return
		}
		params.Status = pgtype.Text{String: *req.Status, Valid: true}
	}
	if req.Title != nil {
		title := service.SanitizeKnowledgeTitle(*req.Title)
		if title == "" {
			writeError(w, http.StatusBadRequest, "title is required")
			return
		}
		normTitle := service.NormalizeKnowledgeTitle(title)
		if normTitle == "" {
			writeError(w, http.StatusBadRequest, "title must contain letters or digits")
			return
		}
		params.Title = pgtype.Text{String: title, Valid: true}
		params.NormTitle = pgtype.Text{String: normTitle, Valid: true}
	}
	if req.Module != nil {
		// Empty string is a legitimate value (clears the module scope);
		// pointer-nil means "keep".
		params.Module = pgtype.Text{String: service.SanitizeKnowledgeModule(*req.Module), Valid: true}
	}
	if req.Body != nil {
		params.Body = pgtype.Text{String: service.SanitizeKnowledgeBody(*req.Body), Valid: true}
	}
	updated, err := h.Queries.UpdateKnowledgeItem(r.Context(), params)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "duplicate of an existing live item")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update knowledge item")
		return
	}
	// Recompile when the change touched an active item (edit/retire) or
	// activated one — those are the cases where the compiled region changes.
	if item.Status == "active" || updated.Status == "active" {
		h.TaskService.RecompileKB(r.Context(), wsUUID, updated.KbName)
	}
	writeJSON(w, http.StatusOK, knowledgeItemToResponse(updated))
}

// DeleteKnowledgeItem hard-deletes an item (archive via PATCH is the
// reversible retire path) and recompiles its KB so the region drops it.
func (h *Handler) DeleteKnowledgeItem(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	itemUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "itemId"), "knowledge item id")
	if !ok {
		return
	}
	// Loaded first for kb_name (needed for the recompile after the row is gone).
	item, err := h.Queries.GetKnowledgeItem(r.Context(), db.GetKnowledgeItemParams{ID: itemUUID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "knowledge item not found")
		return
	}
	rows, err := h.Queries.DeleteKnowledgeItem(r.Context(), db.DeleteKnowledgeItemParams{ID: item.ID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete knowledge item")
		return
	}
	if rows == 0 {
		writeError(w, http.StatusNotFound, "knowledge item not found")
		return
	}
	h.TaskService.RecompileKB(r.Context(), wsUUID, item.KbName)
	w.WriteHeader(http.StatusNoContent)
}
