package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Automations HTTP surface: CRUD for the rules, the run history, the catalogue the
// flow editor renders its node pickers from, and one-click recipe installation.

// AutomationResponse is the wire shape. Conditions/actions are passed through as
// raw JSON so the editor round-trips exactly what it sent — re-encoding through Go
// structs would silently drop a field a newer client added.
type AutomationResponse struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	ProjectID     *string         `json:"project_id"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Enabled       bool            `json:"enabled"`
	TriggerType   string          `json:"trigger_type"`
	TriggerConfig json.RawMessage `json:"trigger_config"`
	Conditions    json.RawMessage `json:"conditions"`
	Actions       json.RawMessage `json:"actions"`
	RecipeKey     string          `json:"recipe_key"`
	RunCount      int32           `json:"run_count"`
	LastRunAt     *string         `json:"last_run_at"`
	CreatedAt     string          `json:"created_at"`
	UpdatedAt     string          `json:"updated_at"`
}

// AutomationRunResponse is one audit row: what fired, whether it applied, and why
// not when it did not.
type AutomationRunResponse struct {
	ID             string          `json:"id"`
	AutomationID   string          `json:"automation_id"`
	IssueID        *string         `json:"issue_id"`
	TriggerType    string          `json:"trigger_type"`
	Status         string          `json:"status"`
	ActionsApplied int32           `json:"actions_applied"`
	Detail         json.RawMessage `json:"detail"`
	Error          string          `json:"error"`
	CreatedAt      string          `json:"created_at"`
}

func automationToResponse(a db.Automation) AutomationResponse {
	resp := AutomationResponse{
		ID:            uuidToString(a.ID),
		WorkspaceID:   uuidToString(a.WorkspaceID),
		Name:          a.Name,
		Description:   a.Description,
		Enabled:       a.Enabled,
		TriggerType:   a.TriggerType,
		TriggerConfig: rawOrEmptyObject(a.TriggerConfig),
		Conditions:    rawOrEmptyArray(a.Conditions),
		Actions:       rawOrEmptyArray(a.Actions),
		RecipeKey:     a.RecipeKey,
		RunCount:      a.RunCount,
		CreatedAt:     timestampString(a.CreatedAt),
		UpdatedAt:     timestampString(a.UpdatedAt),
	}
	if a.ProjectID.Valid {
		id := uuidToString(a.ProjectID)
		resp.ProjectID = &id
	}
	if a.LastRunAt.Valid {
		ts := timestampString(a.LastRunAt)
		resp.LastRunAt = &ts
	}
	return resp
}

func automationRunToResponse(run db.AutomationRun) AutomationRunResponse {
	resp := AutomationRunResponse{
		ID:             uuidToString(run.ID),
		AutomationID:   uuidToString(run.AutomationID),
		TriggerType:    run.TriggerType,
		Status:         run.Status,
		ActionsApplied: run.ActionsApplied,
		Detail:         rawOrEmptyObject(run.Detail),
		Error:          run.Error,
		CreatedAt:      timestampString(run.CreatedAt),
	}
	if run.IssueID.Valid {
		id := uuidToString(run.IssueID)
		resp.IssueID = &id
	}
	return resp
}

// rawOrEmptyObject / rawOrEmptyArray keep the response schema-stable: a NULL or
// empty JSONB column becomes {} / [] rather than a literal `null` the client would
// have to defend against.
func rawOrEmptyObject(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(raw)
}

func rawOrEmptyArray(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`[]`)
	}
	return json.RawMessage(raw)
}

func timestampString(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
}

// automationWriteRequest is the create/update body. The editor always sends the
// whole flow, so this is a full replace (see UpdateAutomation's SQL comment).
type automationWriteRequest struct {
	Name          string                `json:"name"`
	Description   string                `json:"description"`
	Enabled       *bool                 `json:"enabled"`
	ProjectID     *string               `json:"project_id"`
	TriggerType   string                `json:"trigger_type"`
	TriggerConfig map[string]any        `json:"trigger_config"`
	Conditions    []automationCondition `json:"conditions"`
	Actions       []automationAction    `json:"actions"`
}

// ListAutomations handles GET /api/automations.
func (h *Handler) ListAutomations(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	rows, err := h.Queries.ListAutomationsForWorkspace(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list automations")
		return
	}
	resp := make([]AutomationResponse, len(rows))
	for i, row := range rows {
		resp[i] = automationToResponse(row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"automations": resp, "total": len(resp)})
}

// GetAutomation handles GET /api/automations/{id}.
func (h *Handler) GetAutomation(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "automation id")
	if !ok {
		return
	}
	row, err := h.Queries.GetAutomation(r.Context(), db.GetAutomationParams{ID: id, WorkspaceID: parseUUID(workspaceID)})
	if err != nil {
		writeError(w, http.StatusNotFound, "automation not found")
		return
	}
	writeJSON(w, http.StatusOK, automationToResponse(row))
}

// CreateAutomation handles POST /api/automations.
func (h *Handler) CreateAutomation(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	userID := requestUserID(r)

	var req automationWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := validateAutomationRule(req.TriggerType, req.Conditions, req.Actions); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID, ok := h.automationProjectID(w, r, workspaceID, req.ProjectID)
	if !ok {
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	triggerConfig, conditions, actions := automationJSONColumns(req)

	actorID, _ := actorAuthorID(userID)
	row, err := h.Queries.CreateAutomation(r.Context(), db.CreateAutomationParams{
		WorkspaceID:   parseUUID(workspaceID),
		ProjectID:     projectID,
		Name:          strings.TrimSpace(req.Name),
		Description:   strings.TrimSpace(req.Description),
		Enabled:       enabled,
		TriggerType:   strings.TrimSpace(req.TriggerType),
		TriggerConfig: triggerConfig,
		Conditions:    conditions,
		Actions:       actions,
		RecipeKey:     "",
		CreatedByType: "member",
		CreatedByID:   actorID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create automation")
		return
	}
	writeJSON(w, http.StatusCreated, automationToResponse(row))
}

// UpdateAutomation handles PATCH /api/automations/{id}.
func (h *Handler) UpdateAutomation(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "automation id")
	if !ok {
		return
	}
	existing, err := h.Queries.GetAutomation(r.Context(), db.GetAutomationParams{ID: id, WorkspaceID: parseUUID(workspaceID)})
	if err != nil {
		writeError(w, http.StatusNotFound, "automation not found")
		return
	}

	var req automationWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		req.Name = existing.Name
	}
	if strings.TrimSpace(req.TriggerType) == "" {
		req.TriggerType = existing.TriggerType
	}
	if err := validateAutomationRule(req.TriggerType, req.Conditions, req.Actions); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	projectID, ok := h.automationProjectID(w, r, workspaceID, req.ProjectID)
	if !ok {
		return
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	triggerConfig, conditions, actions := automationJSONColumns(req)

	row, err := h.Queries.UpdateAutomation(r.Context(), db.UpdateAutomationParams{
		ID:            id,
		WorkspaceID:   parseUUID(workspaceID),
		Name:          strings.TrimSpace(req.Name),
		Description:   strings.TrimSpace(req.Description),
		Enabled:       enabled,
		ProjectID:     projectID,
		TriggerType:   strings.TrimSpace(req.TriggerType),
		TriggerConfig: triggerConfig,
		Conditions:    conditions,
		Actions:       actions,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update automation")
		return
	}
	writeJSON(w, http.StatusOK, automationToResponse(row))
}

// SetAutomationEnabled handles POST /api/automations/{id}/enabled — the list's
// toggle, kept separate from the full update so flipping a rule off never has to
// round-trip (and potentially rewrite) its flow.
func (h *Handler) SetAutomationEnabled(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "automation id")
	if !ok {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	row, err := h.Queries.SetAutomationEnabled(r.Context(), db.SetAutomationEnabledParams{
		ID: id, WorkspaceID: parseUUID(workspaceID), Enabled: req.Enabled,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "automation not found")
		return
	}
	writeJSON(w, http.StatusOK, automationToResponse(row))
}

// DeleteAutomation handles DELETE /api/automations/{id}.
func (h *Handler) DeleteAutomation(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "automation id")
	if !ok {
		return
	}
	rows, err := h.Queries.DeleteAutomation(r.Context(), db.DeleteAutomationParams{ID: id, WorkspaceID: parseUUID(workspaceID)})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete automation")
		return
	}
	if rows == 0 {
		writeError(w, http.StatusNotFound, "automation not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListAutomationRuns handles GET /api/automations/{id}/runs — the flow's history,
// including the SKIPPED evaluations, because "why did my rule not fire" is the
// question this page exists to answer.
func (h *Handler) ListAutomationRuns(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "automation id")
	if !ok {
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	rows, err := h.Queries.ListAutomationRuns(r.Context(), db.ListAutomationRunsParams{
		AutomationID: id, WorkspaceID: parseUUID(workspaceID), Limit: int32(limit),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list automation runs")
		return
	}
	resp := make([]AutomationRunResponse, len(rows))
	for i, row := range rows {
		resp[i] = automationRunToResponse(row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": resp, "total": len(resp)})
}

// RerunAutomationRun handles POST /api/automations/{id}/runs/{runId}/rerun.
// It retries only the source run's failed steps against the same issue. Steps
// that already succeeded are copied into the new audit row but never executed
// again, so retrying a Telegram failure cannot duplicate an earlier comment,
// webhook, or agent dispatch.
func (h *Handler) RerunAutomationRun(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseUUID(h.resolveWorkspaceID(r))
	automationID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "automation id")
	if !ok {
		return
	}
	runID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runId"), "automation run id")
	if !ok {
		return
	}
	rule, err := h.Queries.GetAutomation(r.Context(), db.GetAutomationParams{
		ID: automationID, WorkspaceID: workspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "automation not found")
		return
	}
	if !rule.Enabled {
		writeError(w, http.StatusConflict, "enable this automation before re-running it")
		return
	}
	source, err := h.Queries.GetAutomationRun(r.Context(), db.GetAutomationRunParams{
		ID: runID, AutomationID: automationID, WorkspaceID: workspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "automation run not found")
		return
	}
	if !source.IssueID.Valid {
		writeError(w, http.StatusConflict, "this run has no task to re-run")
		return
	}
	issue, err := h.Queries.GetIssue(r.Context(), source.IssueID)
	if err != nil || issue.WorkspaceID.Bytes != workspaceID.Bytes {
		writeError(w, http.StatusNotFound, "the run's task no longer exists")
		return
	}
	actions, err := decodeAutomationActions(rule.Actions)
	if err != nil {
		writeError(w, http.StatusConflict, "the automation actions are not valid JSON")
		return
	}
	var previous struct {
		Actions   []automationActionOutcome `json:"actions"`
		ActorType string                    `json:"actor_type"`
		ActorID   string                    `json:"actor_id"`
	}
	if err := json.Unmarshal(source.Detail, &previous); err != nil || len(previous.Actions) != len(actions) {
		writeError(w, http.StatusConflict, "this automation changed after the selected run; wait for a new run before retrying")
		return
	}
	hasFailure := false
	for index, outcome := range previous.Actions {
		if outcome.Type != actions[index].Type {
			writeError(w, http.StatusConflict, "this automation changed after the selected run; wait for a new run before retrying")
			return
		}
		if !outcome.OK {
			hasFailure = true
		}
	}
	if !hasFailure {
		writeError(w, http.StatusConflict, "the selected run has no failed steps to retry")
		return
	}

	ev := AutomationEvent{
		Trigger: source.TriggerType, Issue: issue,
		ActorType: previous.ActorType, ActorID: previous.ActorID,
	}
	unlock := lockIssueQA(uuidToString(issue.ID))
	outcomes, applied, retryErr := h.retryFailedAutomationActions(r.Context(), rule, ev, actions, previous.Actions)
	status, errText := "applied", ""
	if retryErr != nil {
		status, errText = "failed", retryErr.Error()
	}
	created, createErr := h.recordAutomationRunWithMetadata(
		r.Context(), rule, ev, status, applied, outcomes, errText,
		map[string]any{"retry_of": uuidToString(source.ID)},
	)
	unlock()
	if createErr != nil {
		writeError(w, http.StatusInternalServerError, "the retry finished but its audit row could not be saved")
		return
	}
	if err := h.Queries.RecordAutomationFired(r.Context(), rule.ID); err != nil {
		slog.Warn("automation: retry counter bump failed", "error", err, "automation_id", uuidToString(rule.ID))
	}
	writeJSON(w, http.StatusCreated, automationRunToResponse(created))
}

// GetAutomationCatalog handles GET /api/automations/catalog — the node palette the
// flow editor renders: triggers, the fields each one carries (so a condition picker
// can only offer facts that exist for the chosen trigger), step types, operators,
// and the slice-action kinds a dispatch step may fire. Served from the SAME
// registries the engine evaluates, so the editor cannot offer something inert.
func (h *Handler) GetAutomationCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"triggers":             automationTriggerCatalog(),
		"steps":                automationActions,
		"operators":            automationOperatorCatalog(),
		"slice_action_kinds":   automationSliceKinds(),
		"statuses":             []string{"backlog", "todo", "in_progress", "in_review", "done", "cancelled", "blocked"},
		"assign_targets":       []string{"orchestrator", "qa_leader", "reviewer", "agent", "none"},
		"agent_selectors":      []string{"", "orchestrator", "qa_leader", "reviewer", "qa", "agent"},
		"telegram_targets":     []string{"group", "owner"},
		"template_variables":   []string{"{{issue}}", "{{title}}", "{{status}}", "{{automation}}", "{{assignee}}", "{{actor}}", "{{source_url}}", "{{source_assignee}}"},
		"min_interval_default": automationDefaultMinIntervalSeconds,
		"max_per_hour_default": automationDefaultMaxPerHour,
	})
}

// automationProjectID validates an optional project scope. An empty/absent value
// means "every project in the workspace".
func (h *Handler) automationProjectID(w http.ResponseWriter, r *http.Request, workspaceID string, raw *string) (pgtype.UUID, bool) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return pgtype.UUID{}, true
	}
	id, err := parseUUIDErr(strings.TrimSpace(*raw))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return pgtype.UUID{}, false
	}
	project, err := h.Queries.GetProject(r.Context(), id)
	if err != nil || uuidToString(project.WorkspaceID) != workspaceID {
		writeError(w, http.StatusBadRequest, "project is not in this workspace")
		return pgtype.UUID{}, false
	}
	return id, true
}

// automationJSONColumns encodes the three JSONB columns, falling back to empty
// containers so a NULL never reaches the engine's decoders.
func automationJSONColumns(req automationWriteRequest) (triggerConfig, conditions, actions []byte) {
	triggerConfig = []byte(`{}`)
	if len(req.TriggerConfig) > 0 {
		if encoded, err := json.Marshal(req.TriggerConfig); err == nil {
			triggerConfig = encoded
		}
	}
	conditions = []byte(`[]`)
	if len(req.Conditions) > 0 {
		if encoded, err := json.Marshal(req.Conditions); err == nil {
			conditions = encoded
		}
	}
	actions = []byte(`[]`)
	if len(req.Actions) > 0 {
		if encoded, err := json.Marshal(req.Actions); err == nil {
			actions = encoded
		}
	}
	return triggerConfig, conditions, actions
}

// automationTriggerCatalog describes each trigger and the facts it carries, so the
// editor's condition picker is generated rather than hand-maintained.
func automationTriggerCatalog() []map[string]any {
	common := []string{"status", "project_id", "assignee_type", "assignee_id", "priority", "title", "actor_type"}
	fieldsFor := func(extra ...string) []string {
		return append(append([]string{}, extra...), common...)
	}
	return []map[string]any{
		{"type": automationTriggerStageChanged, "fields": fieldsFor("stage", "prev_stage")},
		{"type": automationTriggerStatusChanged, "fields": fieldsFor("from_status", "to_status")},
		{"type": automationTriggerLabelAttached, "fields": fieldsFor("label")},
		{"type": automationTriggerAssigned, "fields": fieldsFor()},
		{"type": automationTriggerIssueCreated, "fields": fieldsFor()},
		{"type": automationTriggerCommentCreated, "fields": fieldsFor("comment_author_type", "comment_body")},
	}
}

func automationOperatorCatalog() []string {
	return []string{
		automationOpEq, automationOpNeq, automationOpIn, automationOpNotIn,
		automationOpContains, automationOpExists, automationOpHasLabel, automationOpNotHasLabel,
	}
}

// automationSliceKinds lists the agent actions a dispatch step may fire, filtered
// to the ones that make sense unattended (a design/docs authoring action is fine;
// nothing here can merge or deploy).
func automationSliceKinds() []string {
	return []string{
		sliceActionRunReview, sliceActionRunQA, sliceActionGenTests, sliceActionRunTests,
		sliceActionCompileTests, sliceActionCommitTests, sliceActionOpenPR, sliceActionRunCI,
		sliceActionWriteTests, sliceActionWriteDocs, sliceActionAutoDocs, sliceActionReviewPart,
		sliceActionDraftCode, sliceActionDesignAudit,
	}
}
