package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/integrations/zohocrm"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// The workspace Zoho connection is the credential root of the dynamic Zoho
// integration (docs/zoho-dynamic-integration.md): one OAuth client +
// refresh token per workspace, sealed at rest with AGORA_ZOHO_SECRET_KEY.
// Plaintext is decrypted server-side only — discovery proxy calls and the
// sync engine — and never returned by any endpoint. If the key is unset the
// write endpoints fail closed (503) rather than store plaintext.

var (
	zohoBoxOnce sync.Once
	zohoBoxVal  *secretbox.Box
	zohoBoxErr  error
)

func zohoConnectionBox() (*secretbox.Box, error) {
	zohoBoxOnce.Do(func() {
		key, err := secretbox.LoadKey("AGORA_ZOHO_SECRET_KEY")
		if err != nil {
			zohoBoxErr = err
			return
		}
		zohoBoxVal, zohoBoxErr = secretbox.New(key)
	})
	return zohoBoxVal, zohoBoxErr
}

// zohoConnActivityUpdated / zohoConnActivityDeleted are the activity_log
// action constants for connection mutations. Details carry dc/org ids and
// the actor — never client_secret or refresh_token material.
const (
	zohoConnActivityUpdated = "zoho_connection_updated"
	zohoConnActivityDeleted = "zoho_connection_deleted"
)

type zohoConnectionStatusResponse struct {
	Configured  bool   `json:"configured"`
	DC          string `json:"dc,omitempty"`
	ClientID    string `json:"client_id,omitempty"`
	Scopes      string `json:"scopes,omitempty"`
	CrmOrgID    string `json:"crm_org_id,omitempty"`
	DeskOrgID   string `json:"desk_org_id,omitempty"`
	ProbeStatus string `json:"probe_status,omitempty"`
	ProbedAt    string `json:"probed_at,omitempty"`
}

type putZohoConnectionRequest struct {
	DC               string `json:"dc"`
	ClientID         string `json:"client_id"`
	ClientSecret     string `json:"client_secret"`
	RefreshToken     string `json:"refresh_token"`
	Scopes           string `json:"scopes"`
	CrmOrgID         string `json:"crm_org_id"`
	DeskOrgID        string `json:"desk_org_id"`
	ProjectsPortalID string `json:"projects_portal_id"`
	SprintsTeamID    string `json:"sprints_team_id"`
}

// authorizeZohoConnectionWrite gates the mutating connection endpoints:
// humans only (agent actors rejected — a compromised agent must not be able
// to swap the workspace's Zoho identity), owner/admin role.
func (h *Handler) authorizeZohoConnectionWrite(w http.ResponseWriter, r *http.Request) (pgtype.UUID, db.Member, bool) {
	id := workspaceIDFromURL(r, "id")
	idUUID, ok := parseUUIDOrBadRequest(w, id, "workspace id")
	if !ok {
		return pgtype.UUID{}, db.Member{}, false
	}
	actorType, _ := h.resolveActor(r, requestUserID(r), id)
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents may not manage the Zoho connection")
		return pgtype.UUID{}, db.Member{}, false
	}
	member, ok := h.requireWorkspaceRole(w, r, id, "workspace not found", "owner", "admin")
	if !ok {
		return pgtype.UUID{}, db.Member{}, false
	}
	return idUUID, member, true
}

// classifyZohoProbe maps a probe outcome to the stored probe_status and
// whether the save must be rejected. Only a definite credential rejection
// blocks the save; transport errors and Zoho-side outages store as
// "unreachable" so an outage cannot block credential rotation.
func classifyZohoProbe(err error) (probeStatus string, credInvalid bool) {
	switch {
	case err == nil:
		return "ok", false
	case zohocrm.IsAuthError(err):
		return "invalid", true
	default:
		return "unreachable", false
	}
}

// newZohoCRMClientFromParts builds a CRM client honouring the test host
// overrides (httptest servers set these; empty in production so hosts derive
// from dc).
func newZohoCRMClientFromParts(clientID, clientSecret, refreshToken, dc string) (*zohocrm.Client, error) {
	return zohocrm.New(clientID, clientSecret, refreshToken, dc,
		os.Getenv("ZOHO_DYN_ACCOUNTS_BASE"), os.Getenv("ZOHO_DYN_API_BASE"))
}

// PutZohoConnection saves (or rotates) the workspace's Zoho connection. The
// grant is probed against CRM /org before sealing so a mistyped refresh
// token is rejected with 422 instead of silently breaking discovery + sync.
func (h *Handler) PutZohoConnection(w http.ResponseWriter, r *http.Request) {
	wsUUID, member, ok := h.authorizeZohoConnectionWrite(w, r)
	if !ok {
		return
	}
	var req putZohoConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.DC = strings.TrimSpace(strings.ToLower(req.DC))
	if req.DC == "" {
		req.DC = "us"
	}
	if !zohocrm.KnownDC(req.DC) {
		writeError(w, http.StatusBadRequest, "dc must be one of: us, eu, in, au, jp, sa, ca")
		return
	}
	req.ClientID = strings.TrimSpace(req.ClientID)
	req.ClientSecret = strings.TrimSpace(req.ClientSecret)
	req.RefreshToken = strings.TrimSpace(req.RefreshToken)
	if req.ClientID == "" || req.ClientSecret == "" || req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "client_id, client_secret and refresh_token are required")
		return
	}

	box, err := zohoConnectionBox()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "zoho connections are not configured on this server (AGORA_ZOHO_SECRET_KEY unset)")
		return
	}

	client, err := newZohoCRMClientFromParts(req.ClientID, req.ClientSecret, req.RefreshToken, req.DC)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Probe via CurrentUser, not /org: the recommended scope superset grants
	// ZohoCRM.users.READ but not ZohoCRM.org.READ, so an /org probe reports a
	// perfectly valid grant as "unreachable" (OAUTH_SCOPE_MISMATCH is an HTTP
	// 401, indistinguishable from an outage at this layer).
	probeCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	_, probeErr := client.GetCurrentUser(probeCtx)
	cancel()
	probeStatus, credInvalid := classifyZohoProbe(probeErr)
	if credInvalid {
		writeError(w, http.StatusUnprocessableEntity, "zoho_credentials_invalid: Zoho rejected the grant (accounts token mint)")
		return
	}

	sealedSecret, err := box.Seal([]byte(req.ClientSecret))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to seal client secret")
		return
	}
	sealedRefresh, err := box.Seal([]byte(req.RefreshToken))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to seal refresh token")
		return
	}
	creator, ok := parseUUIDOrBadRequest(w, requestUserID(r), "user id")
	if !ok {
		return
	}

	// Persist + audit commit together or not at all (workspace_mcp / MUL-2600
	// contract): an audit outage cannot leave an unaudited credential swap.
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save zoho connection")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	row, err := qtx.UpsertZohoConnection(r.Context(), db.UpsertZohoConnectionParams{
		WorkspaceID:           wsUUID,
		Dc:                    req.DC,
		ClientID:              req.ClientID,
		ClientSecretEncrypted: sealedSecret,
		RefreshTokenEncrypted: sealedRefresh,
		Scopes:                strings.TrimSpace(req.Scopes),
		CrmOrgID:              strings.TrimSpace(req.CrmOrgID),
		DeskOrgID:             strings.TrimSpace(req.DeskOrgID),
		ProjectsPortalID:      strings.TrimSpace(req.ProjectsPortalID),
		SprintsTeamID:         strings.TrimSpace(req.SprintsTeamID),
		ProbeStatus:           probeStatus,
		CreatedBy:             creator,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save zoho connection")
		return
	}

	details, _ := json.Marshal(map[string]any{
		"dc": row.Dc, "client_id": row.ClientID, "probe_status": probeStatus,
	})
	if _, err := qtx.CreateActivity(r.Context(), db.CreateActivityParams{
		WorkspaceID: wsUUID,
		IssueID:     pgtype.UUID{},
		ActorType:   pgtype.Text{String: "member", Valid: true},
		ActorID:     parseUUID(uuidToString(member.UserID)),
		Action:      zohoConnActivityUpdated,
		Details:     details,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "audit log write failed; connection update rolled back")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save zoho connection")
		return
	}

	writeJSON(w, http.StatusOK, zohoConnectionStatusFromRow(row))
}

// GetZohoConnectionStatus is member-visible (the integrations tab must render
// for non-admins) and never returns secret material.
func (h *Handler) GetZohoConnectionStatus(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	row, err := h.Queries.GetZohoConnectionForWorkspace(r.Context(), wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, zohoConnectionStatusResponse{Configured: false})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load zoho connection")
		return
	}
	writeJSON(w, http.StatusOK, zohoConnectionStatusFromRow(row))
}

func zohoConnectionStatusFromRow(row db.ZohoConnection) zohoConnectionStatusResponse {
	resp := zohoConnectionStatusResponse{
		Configured:  true,
		DC:          row.Dc,
		ClientID:    row.ClientID,
		Scopes:      row.Scopes,
		CrmOrgID:    row.CrmOrgID,
		DeskOrgID:   row.DeskOrgID,
		ProbeStatus: row.ProbeStatus,
	}
	if row.ProbedAt.Valid {
		resp.ProbedAt = row.ProbedAt.Time.UTC().Format(time.RFC3339)
	}
	return resp
}

// DeleteZohoConnection removes the workspace's connection. Sync configs
// cascade via FK when the dynamic engine lands (D2).
func (h *Handler) DeleteZohoConnection(w http.ResponseWriter, r *http.Request) {
	wsUUID, member, ok := h.authorizeZohoConnectionWrite(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.DeleteZohoConnection(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete zoho connection")
		return
	}
	if rows == 0 {
		writeError(w, http.StatusNotFound, "zoho connection not found")
		return
	}
	details, _ := json.Marshal(map[string]any{"workspace_id": uuidToString(wsUUID)})
	_, _ = h.Queries.CreateActivity(r.Context(), db.CreateActivityParams{
		WorkspaceID: wsUUID,
		IssueID:     pgtype.UUID{},
		ActorType:   pgtype.Text{String: "member", Valid: true},
		ActorID:     parseUUID(uuidToString(member.UserID)),
		Action:      zohoConnActivityDeleted,
		Details:     details,
	})
	w.WriteHeader(http.StatusNoContent)
}

// zohoCRMClientForWorkspace decrypts the workspace connection into a live CRM
// client — the server-side consumer used by discovery (D1) and the sync
// engine (D2). ok=false when no connection, sealing key unset, or decryption
// fails.
func (h *Handler) zohoCRMClientForWorkspace(ctx context.Context, wsUUID pgtype.UUID) (*zohocrm.Client, bool) {
	row, err := h.Queries.GetZohoConnectionForWorkspace(ctx, wsUUID)
	if err != nil {
		return nil, false
	}
	box, err := zohoConnectionBox()
	if err != nil {
		return nil, false
	}
	secret, err := box.Open(row.ClientSecretEncrypted)
	if err != nil {
		return nil, false
	}
	refresh, err := box.Open(row.RefreshTokenEncrypted)
	if err != nil {
		return nil, false
	}
	client, err := newZohoCRMClientFromParts(row.ClientID, string(secret), string(refresh), row.Dc)
	if err != nil {
		return nil, false
	}
	return client, true
}

// ListZohoCRMModules proxies CRM module discovery for the connected
// workspace: the raw material for the dynamic sync UI (operator picks which
// modules become sync configs). Owner/admin — this is a configuration
// surface, and module names can reveal business structure.
func (h *Handler) ListZohoCRMModules(w http.ResponseWriter, r *http.Request) {
	wsUUID, _, ok := h.authorizeZohoConnectionWrite(w, r)
	if !ok {
		return
	}
	client, ok := h.zohoCRMClientForWorkspace(r.Context(), wsUUID)
	if !ok {
		writeError(w, http.StatusBadRequest, "zoho connection not configured for this workspace")
		return
	}
	modules, err := client.ListModules(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "zoho modules: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"modules": modules})
}

// ListZohoCRMFields proxies field discovery for one module — feeds the
// field-map / status-map editor.
func (h *Handler) ListZohoCRMFields(w http.ResponseWriter, r *http.Request) {
	wsUUID, _, ok := h.authorizeZohoConnectionWrite(w, r)
	if !ok {
		return
	}
	module := strings.TrimSpace(r.URL.Query().Get("module"))
	if module == "" {
		writeError(w, http.StatusBadRequest, "module query parameter is required")
		return
	}
	client, ok := h.zohoCRMClientForWorkspace(r.Context(), wsUUID)
	if !ok {
		writeError(w, http.StatusBadRequest, "zoho connection not configured for this workspace")
		return
	}
	fields, err := client.ListFields(r.Context(), module)
	if err != nil {
		writeError(w, http.StatusBadGateway, "zoho fields: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"module": module, "fields": fields})
}
