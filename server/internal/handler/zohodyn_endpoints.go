package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Sync-config CRUD for the dynamic Zoho engine (design doc §1.3). Same auth
// contract as the connection endpoints they configure: workspace owner/admin
// only, agent actors rejected (authorizeZohoConnectionWrite), and every
// mutation audited in the same transaction as the write (workspace_mcp /
// MUL-2600 contract). Audit details carry the module name only — field and
// status maps are not secrets but they are noise, and the activity row is a
// who-touched-what ledger, not a diff store.

const (
	zohoSyncConfigActivityCreated = "zoho_sync_config_created"
	zohoSyncConfigActivityUpdated = "zoho_sync_config_updated"
	zohoSyncConfigActivityDeleted = "zoho_sync_config_deleted"
)

var zohoDynDirections = map[string]bool{"in": true, "out": true, "both": true}

type zohoSyncConfigResponse struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	ConnectionID  string          `json:"connection_id"`
	Channel       string          `json:"channel"`
	ModuleAPIName string          `json:"module_api_name"`
	ProjectID     string          `json:"project_id,omitempty"`
	Enabled       bool            `json:"enabled"`
	Direction     string          `json:"direction"`
	FieldMap      json.RawMessage `json:"field_map"`
	StatusMap     json.RawMessage `json:"status_map"`
	FilterCOQL    string          `json:"filter_coql"`
	Cursor        string          `json:"cursor,omitempty"`
	CreatedAt     string          `json:"created_at"`
	UpdatedAt     string          `json:"updated_at"`
}

func zohoSyncConfigToResponse(row db.ZohoSyncConfig) zohoSyncConfigResponse {
	resp := zohoSyncConfigResponse{
		ID:            uuidToString(row.ID),
		WorkspaceID:   uuidToString(row.WorkspaceID),
		ConnectionID:  uuidToString(row.ConnectionID),
		Channel:       row.Channel,
		ModuleAPIName: row.ModuleApiName,
		Enabled:       row.Enabled,
		Direction:     row.Direction,
		FieldMap:      json.RawMessage(row.FieldMap),
		StatusMap:     json.RawMessage(row.StatusMap),
		FilterCOQL:    row.FilterCoql,
		CreatedAt:     row.CreatedAt.Time.UTC().Format(time.RFC3339),
		UpdatedAt:     row.UpdatedAt.Time.UTC().Format(time.RFC3339),
	}
	if row.ProjectID.Valid {
		resp.ProjectID = uuidToString(row.ProjectID)
	}
	if row.Cursor.Valid {
		resp.Cursor = row.Cursor.Time.UTC().Format(time.RFC3339)
	}
	return resp
}

// zohoSyncConfigRequest is the shared create/update body. Pointers (and raw
// JSON presence) distinguish "absent" from "set to zero value" so PUT can be
// a partial update.
type zohoSyncConfigRequest struct {
	ModuleAPIName string          `json:"module_api_name"`
	ProjectID     *string         `json:"project_id"`
	Enabled       *bool           `json:"enabled"`
	Direction     *string         `json:"direction"`
	FieldMap      json.RawMessage `json:"field_map"`
	StatusMap     json.RawMessage `json:"status_map"`
	FilterCOQL    *string         `json:"filter_coql"`
}

// validateZohoDynFieldMap enforces the §4 whitelist at write time: keys must
// be whitelisted Agora fields, values must be identifier-shaped Zoho field
// API names (the same regex the COQL builder re-checks).
func validateZohoDynFieldMap(raw json.RawMessage) (string, bool) {
	if len(jsonValueOrNil(raw)) == 0 {
		return "", true
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "field_map must be a JSON object", false
	}
	for k, v := range m {
		if !zohoDynAgoraFields[k] {
			return "field_map key " + zohoDynQuote(k) + " is not an allowed Agora field (title, description, priority, due_date, status)", false
		}
		s, ok := v.(string)
		if !ok {
			return "field_map value for " + zohoDynQuote(k) + " must be a string (Zoho field API name)", false
		}
		if !zohoDynIdentifierRe.MatchString(strings.TrimSpace(s)) {
			return "field_map value for " + zohoDynQuote(k) + " must be a Zoho field API name (letters, digits, underscore)", false
		}
	}
	return "", true
}

// validateZohoDynStatusMap enforces the status_map shape: an object with only
// "in" / "out" keys; "in" maps Zoho status strings to valid Agora statuses,
// "out" maps valid Agora statuses to non-empty Zoho status strings.
func validateZohoDynStatusMap(raw json.RawMessage) (string, bool) {
	if len(jsonValueOrNil(raw)) == 0 {
		return "", true
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "status_map must be a JSON object", false
	}
	for key := range m {
		if key != "in" && key != "out" {
			return "status_map may only contain \"in\" and \"out\" objects", false
		}
	}
	if inAny, ok := m["in"]; ok {
		in, ok := inAny.(map[string]any)
		if !ok {
			return `status_map "in" must be an object of Zoho status -> Agora status`, false
		}
		for zoho, agora := range in {
			s, ok := agora.(string)
			if !ok || !zohoDynAgoraStatuses[s] {
				return "status_map \"in\" value for " + zohoDynQuote(zoho) + " must be a valid Agora status", false
			}
		}
	}
	if outAny, ok := m["out"]; ok {
		out, ok := outAny.(map[string]any)
		if !ok {
			return `status_map "out" must be an object of Agora status -> Zoho status`, false
		}
		for agora, zoho := range out {
			if !zohoDynAgoraStatuses[agora] {
				return "status_map \"out\" key " + zohoDynQuote(agora) + " must be a valid Agora status", false
			}
			s, ok := zoho.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return "status_map \"out\" value for " + zohoDynQuote(agora) + " must be a non-empty string", false
			}
		}
	}
	return "", true
}

// zohoDynQuote quotes a user-supplied key for an error message.
func zohoDynQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// resolveZohoDynProjectID validates an optional project_id against the
// workspace. ok=false means a response was already written.
func (h *Handler) resolveZohoDynProjectID(w http.ResponseWriter, r *http.Request, wsUUID pgtype.UUID, raw string) (pgtype.UUID, bool) {
	pid, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(raw), "project id")
	if !ok {
		return pgtype.UUID{}, false
	}
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID:          pid,
		WorkspaceID: wsUUID,
	}); err != nil {
		writeError(w, http.StatusBadRequest, "project not found in this workspace")
		return pgtype.UUID{}, false
	}
	return pid, true
}

// auditZohoSyncConfig writes the tx-coupled activity row for a config
// mutation. Details carry the module name only.
func auditZohoSyncConfig(r *http.Request, qtx *db.Queries, wsUUID pgtype.UUID, member db.Member, action, module string) error {
	details, _ := json.Marshal(map[string]any{"module_api_name": module})
	_, err := qtx.CreateActivity(r.Context(), db.CreateActivityParams{
		WorkspaceID: wsUUID,
		IssueID:     pgtype.UUID{},
		ActorType:   pgtype.Text{String: "member", Valid: true},
		ActorID:     parseUUID(uuidToString(member.UserID)),
		Action:      action,
		Details:     details,
	})
	return err
}

// ListZohoSyncConfigs returns the workspace's sync configs. Field/status maps
// are included — they are configuration, not secrets; the connection's OAuth
// material never appears here.
func (h *Handler) ListZohoSyncConfigs(w http.ResponseWriter, r *http.Request) {
	wsUUID, _, ok := h.authorizeZohoConnectionWrite(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListZohoSyncConfigsForWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list zoho sync configs")
		return
	}
	out := make([]zohoSyncConfigResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, zohoSyncConfigToResponse(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"configs": out})
}

// CreateZohoSyncConfig creates a sync config for one CRM module. Requires an
// existing workspace Zoho connection; module/direction/maps are validated
// before anything is written.
func (h *Handler) CreateZohoSyncConfig(w http.ResponseWriter, r *http.Request) {
	wsUUID, member, ok := h.authorizeZohoConnectionWrite(w, r)
	if !ok {
		return
	}
	var req zohoSyncConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	module := strings.TrimSpace(req.ModuleAPIName)
	if !zohoDynIdentifierRe.MatchString(module) {
		writeError(w, http.StatusBadRequest, "module_api_name must be 1-100 letters, digits or underscores")
		return
	}
	conn, err := h.Queries.GetZohoConnectionForWorkspace(r.Context(), wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusBadRequest, "zoho connection not configured for this workspace")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load zoho connection")
		return
	}
	direction := "both"
	if req.Direction != nil {
		direction = strings.TrimSpace(*req.Direction)
		if !zohoDynDirections[direction] {
			writeError(w, http.StatusBadRequest, "direction must be one of: in, out, both")
			return
		}
	}
	if msg, ok := validateZohoDynFieldMap(req.FieldMap); !ok {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if msg, ok := validateZohoDynStatusMap(req.StatusMap); !ok {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	projectID := pgtype.UUID{}
	if req.ProjectID != nil && strings.TrimSpace(*req.ProjectID) != "" {
		pid, ok := h.resolveZohoDynProjectID(w, r, wsUUID, *req.ProjectID)
		if !ok {
			return
		}
		projectID = pid
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	fieldMap := []byte(`{}`)
	if b := jsonValueOrNil(req.FieldMap); len(b) > 0 {
		fieldMap = b
	}
	statusMap := []byte(`{}`)
	if b := jsonValueOrNil(req.StatusMap); len(b) > 0 {
		statusMap = b
	}
	filter := ""
	if req.FilterCOQL != nil {
		filter = strings.TrimSpace(*req.FilterCOQL)
	}

	// Persist + audit commit together or not at all (MUL-2600 contract).
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create zoho sync config")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	row, err := qtx.CreateZohoSyncConfig(r.Context(), db.CreateZohoSyncConfigParams{
		WorkspaceID:   wsUUID,
		ConnectionID:  conn.ID,
		Channel:       zohoDynChannel,
		ModuleApiName: module,
		ProjectID:     projectID,
		Enabled:       enabled,
		Direction:     direction,
		FieldMap:      fieldMap,
		StatusMap:     statusMap,
		FilterCoql:    filter,
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a sync config for this module already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create zoho sync config")
		return
	}
	if err := auditZohoSyncConfig(r, qtx, wsUUID, member, zohoSyncConfigActivityCreated, module); err != nil {
		writeError(w, http.StatusInternalServerError, "audit log write failed; sync config create rolled back")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create zoho sync config")
		return
	}
	writeJSON(w, http.StatusCreated, zohoSyncConfigToResponse(row))
}

// loadZohoSyncConfigForWorkspace resolves {configId} and enforces the
// cross-workspace boundary: a config belonging to another workspace is
// indistinguishable from a missing one (404).
func (h *Handler) loadZohoSyncConfigForWorkspace(w http.ResponseWriter, r *http.Request, wsUUID pgtype.UUID) (db.ZohoSyncConfig, bool) {
	cfgID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "configId"), "config id")
	if !ok {
		return db.ZohoSyncConfig{}, false
	}
	cfg, err := h.Queries.GetZohoSyncConfig(r.Context(), cfgID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "zoho sync config not found")
			return db.ZohoSyncConfig{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load zoho sync config")
		return db.ZohoSyncConfig{}, false
	}
	if cfg.WorkspaceID != wsUUID {
		writeError(w, http.StatusNotFound, "zoho sync config not found")
		return db.ZohoSyncConfig{}, false
	}
	return cfg, true
}

// UpdateZohoSyncConfig partially updates a sync config (COALESCE narg —
// absent fields keep their value). module_api_name is immutable; delete and
// recreate to re-point a config.
func (h *Handler) UpdateZohoSyncConfig(w http.ResponseWriter, r *http.Request) {
	wsUUID, member, ok := h.authorizeZohoConnectionWrite(w, r)
	if !ok {
		return
	}
	cfg, ok := h.loadZohoSyncConfigForWorkspace(w, r, wsUUID)
	if !ok {
		return
	}
	var req zohoSyncConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	params := db.UpdateZohoSyncConfigParams{ID: cfg.ID}
	if req.Direction != nil {
		d := strings.TrimSpace(*req.Direction)
		if !zohoDynDirections[d] {
			writeError(w, http.StatusBadRequest, "direction must be one of: in, out, both")
			return
		}
		params.Direction = pgtype.Text{String: d, Valid: true}
	}
	if req.Enabled != nil {
		params.Enabled = pgtype.Bool{Bool: *req.Enabled, Valid: true}
	}
	if b := jsonValueOrNil(req.FieldMap); len(b) > 0 {
		if msg, ok := validateZohoDynFieldMap(req.FieldMap); !ok {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		params.FieldMap = b
	}
	if b := jsonValueOrNil(req.StatusMap); len(b) > 0 {
		if msg, ok := validateZohoDynStatusMap(req.StatusMap); !ok {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		params.StatusMap = b
	}
	if req.FilterCOQL != nil {
		params.FilterCoql = pgtype.Text{String: strings.TrimSpace(*req.FilterCOQL), Valid: true}
	}
	if req.ProjectID != nil && strings.TrimSpace(*req.ProjectID) != "" {
		pid, ok := h.resolveZohoDynProjectID(w, r, wsUUID, *req.ProjectID)
		if !ok {
			return
		}
		params.ProjectID = pid
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update zoho sync config")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	row, err := qtx.UpdateZohoSyncConfig(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update zoho sync config")
		return
	}
	if err := auditZohoSyncConfig(r, qtx, wsUUID, member, zohoSyncConfigActivityUpdated, cfg.ModuleApiName); err != nil {
		writeError(w, http.StatusInternalServerError, "audit log write failed; sync config update rolled back")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update zoho sync config")
		return
	}
	writeJSON(w, http.StatusOK, zohoSyncConfigToResponse(row))
}

// DeleteZohoSyncConfig removes a sync config. The delete is workspace-scoped
// in SQL, so a raced or cross-workspace id deletes zero rows → 404.
func (h *Handler) DeleteZohoSyncConfig(w http.ResponseWriter, r *http.Request) {
	wsUUID, member, ok := h.authorizeZohoConnectionWrite(w, r)
	if !ok {
		return
	}
	cfg, ok := h.loadZohoSyncConfigForWorkspace(w, r, wsUUID)
	if !ok {
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete zoho sync config")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	rows, err := qtx.DeleteZohoSyncConfig(r.Context(), db.DeleteZohoSyncConfigParams{
		ID:          cfg.ID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete zoho sync config")
		return
	}
	if rows == 0 {
		writeError(w, http.StatusNotFound, "zoho sync config not found")
		return
	}
	if err := auditZohoSyncConfig(r, qtx, wsUUID, member, zohoSyncConfigActivityDeleted, cfg.ModuleApiName); err != nil {
		writeError(w, http.StatusInternalServerError, "audit log write failed; sync config delete rolled back")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete zoho sync config")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
