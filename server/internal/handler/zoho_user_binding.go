package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/integrations/zohocrm"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Per-user Zoho identity binding (U1, docs/zoho-dynamic-integration.md):
// a member connects their OWN Zoho account so agents acting for them call
// Zoho as that person. The grant is minted under the workspace connection's
// OAuth client via a self-client grant code the member pastes; the resulting
// refresh token is sealed with the same box as the workspace connection.
// Self-service by design: any member manages their own binding (and only
// their own), unlike the owner/admin-gated workspace connection.

const (
	zohoBindingActivityUpdated = "zoho_user_binding_updated"
	zohoBindingActivityDeleted = "zoho_user_binding_deleted"
)

type zohoUserBindingStatusResponse struct {
	Bound         bool   `json:"bound"`
	ZohoUserEmail string `json:"zoho_user_email,omitempty"`
	Scopes        string `json:"scopes,omitempty"`
	ProbeStatus   string `json:"probe_status,omitempty"`
	ProbedAt      string `json:"probed_at,omitempty"`
}

type putZohoUserBindingRequest struct {
	GrantCode string `json:"grant_code"`
}

// authorizeZohoUserBinding gates the binding endpoints: any workspace member
// (self-service), but never an agent actor — an agent must not be able to
// re-point its host user's Zoho identity at an attacker-controlled account.
func (h *Handler) authorizeZohoUserBinding(w http.ResponseWriter, r *http.Request) (pgtype.UUID, db.Member, bool) {
	id := workspaceIDFromURL(r, "id")
	idUUID, ok := parseUUIDOrBadRequest(w, id, "workspace id")
	if !ok {
		return pgtype.UUID{}, db.Member{}, false
	}
	actorType, _ := h.resolveActor(r, requestUserID(r), id)
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents may not manage Zoho user bindings")
		return pgtype.UUID{}, db.Member{}, false
	}
	member, ok := h.requireWorkspaceRole(w, r, id, "workspace not found", "owner", "admin", "member")
	if !ok {
		return pgtype.UUID{}, db.Member{}, false
	}
	return idUUID, member, true
}

// PutZohoUserBinding exchanges a pasted self-client grant code for the
// caller's refresh token and seals it. The exchange runs under the workspace
// connection's OAuth client — a refresh token is bound to the (client, Zoho
// user) pair — so a workspace connection must exist first.
func (h *Handler) PutZohoUserBinding(w http.ResponseWriter, r *http.Request) {
	wsUUID, member, ok := h.authorizeZohoUserBinding(w, r)
	if !ok {
		return
	}
	var req putZohoUserBindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.GrantCode = strings.TrimSpace(req.GrantCode)
	if req.GrantCode == "" {
		writeError(w, http.StatusBadRequest, "grant_code is required")
		return
	}

	conn, err := h.Queries.GetZohoConnectionForWorkspace(r.Context(), wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusBadRequest, "workspace zoho connection must be configured before binding user accounts")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load zoho connection")
		return
	}
	box, err := zohoConnectionBox()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "zoho connections are not configured on this server (AGORA_ZOHO_SECRET_KEY unset)")
		return
	}
	clientSecret, err := box.Open(conn.ClientSecretEncrypted)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to unseal workspace connection")
		return
	}

	grant, err := zohocrm.ExchangeGrantCode(r.Context(), conn.ClientID, string(clientSecret),
		req.GrantCode, conn.Dc, os.Getenv("ZOHO_DYN_ACCOUNTS_BASE"))
	if err != nil {
		if zohocrm.IsAuthError(err) {
			writeError(w, http.StatusUnprocessableEntity, "zoho_grant_invalid: Zoho rejected the grant code (codes are single-use and expire within minutes)")
			return
		}
		writeError(w, http.StatusBadGateway, "zoho grant exchange failed: "+err.Error())
		return
	}

	// Identity probe: resolve who this grant actually belongs to. Best-effort
	// for the email hint, but a definite auth rejection blocks the save.
	zohoEmail, probeStatus := "", "ok"
	if client, cerr := newZohoCRMClientFromParts(conn.ClientID, string(clientSecret), grant.RefreshToken, conn.Dc); cerr == nil {
		probeCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		user, uerr := client.GetCurrentUser(probeCtx)
		cancel()
		switch {
		case uerr == nil:
			zohoEmail = user.Email
		case zohocrm.IsAuthError(uerr):
			writeError(w, http.StatusUnprocessableEntity, "zoho_grant_invalid: minted token was rejected by Zoho CRM")
			return
		default:
			probeStatus = "unreachable"
		}
	}

	sealed, err := box.Seal([]byte(grant.RefreshToken))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to seal refresh token")
		return
	}

	// Persist + audit commit together (workspace_mcp / MUL-2600 contract).
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save zoho user binding")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	row, err := qtx.UpsertZohoUserBinding(r.Context(), db.UpsertZohoUserBindingParams{
		WorkspaceID:           wsUUID,
		UserID:                member.UserID,
		ConnectionID:          conn.ID,
		RefreshTokenEncrypted: sealed,
		Scopes:                grant.Scope,
		ZohoUserEmail:         zohoEmail,
		ProbeStatus:           probeStatus,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save zoho user binding")
		return
	}
	details, _ := json.Marshal(map[string]any{
		"zoho_user_email": zohoEmail, "probe_status": probeStatus,
	})
	if _, err := qtx.CreateActivity(r.Context(), db.CreateActivityParams{
		WorkspaceID: wsUUID,
		IssueID:     pgtype.UUID{},
		ActorType:   pgtype.Text{String: "member", Valid: true},
		ActorID:     parseUUID(uuidToString(member.UserID)),
		Action:      zohoBindingActivityUpdated,
		Details:     details,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "audit log write failed; binding rolled back")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save zoho user binding")
		return
	}

	writeJSON(w, http.StatusOK, zohoUserBindingStatusFromRow(row))
}

// GetZohoUserBindingStatus returns the CALLER's own binding state — never
// another member's, and never secret material.
func (h *Handler) GetZohoUserBindingStatus(w http.ResponseWriter, r *http.Request) {
	wsUUID, member, ok := h.authorizeZohoUserBinding(w, r)
	if !ok {
		return
	}
	row, err := h.Queries.GetZohoUserBinding(r.Context(), db.GetZohoUserBindingParams{
		WorkspaceID: wsUUID,
		UserID:      member.UserID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, zohoUserBindingStatusResponse{Bound: false})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load zoho user binding")
		return
	}
	writeJSON(w, http.StatusOK, zohoUserBindingStatusFromRow(row))
}

func zohoUserBindingStatusFromRow(row db.ZohoUserBinding) zohoUserBindingStatusResponse {
	resp := zohoUserBindingStatusResponse{
		Bound:         true,
		ZohoUserEmail: row.ZohoUserEmail,
		Scopes:        row.Scopes,
		ProbeStatus:   row.ProbeStatus,
	}
	if row.ProbedAt.Valid {
		resp.ProbedAt = row.ProbedAt.Time.UTC().Format(time.RFC3339)
	}
	return resp
}

// DeleteZohoUserBinding removes the CALLER's own binding.
func (h *Handler) DeleteZohoUserBinding(w http.ResponseWriter, r *http.Request) {
	wsUUID, member, ok := h.authorizeZohoUserBinding(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.DeleteZohoUserBinding(r.Context(), db.DeleteZohoUserBindingParams{
		WorkspaceID: wsUUID,
		UserID:      member.UserID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete zoho user binding")
		return
	}
	if rows == 0 {
		writeError(w, http.StatusNotFound, "zoho user binding not found")
		return
	}
	_, _ = h.Queries.CreateActivity(r.Context(), db.CreateActivityParams{
		WorkspaceID: wsUUID,
		IssueID:     pgtype.UUID{},
		ActorType:   pgtype.Text{String: "member", Valid: true},
		ActorID:     parseUUID(uuidToString(member.UserID)),
		Action:      zohoBindingActivityDeleted,
		Details:     []byte(`{}`),
	})
	w.WriteHeader(http.StatusNoContent)
}

// zohoCRMClientForUser decrypts one member's binding into a live CRM client
// acting AS THAT USER — the identity resolution consumed by the MCP proxy
// (U3) and any acting-user Zoho call. ok=false when the user has no binding,
// the workspace has no connection, sealing is unavailable, or the binding's
// last probe flagged it invalid.
func (h *Handler) zohoCRMClientForUser(ctx context.Context, wsUUID, userUUID pgtype.UUID) (*zohocrm.Client, bool) {
	binding, err := h.Queries.GetZohoUserBinding(ctx, db.GetZohoUserBindingParams{
		WorkspaceID: wsUUID,
		UserID:      userUUID,
	})
	if err != nil || binding.ProbeStatus == "invalid" {
		return nil, false
	}
	conn, err := h.Queries.GetZohoConnectionForWorkspace(ctx, wsUUID)
	if err != nil {
		return nil, false
	}
	box, err := zohoConnectionBox()
	if err != nil {
		return nil, false
	}
	clientSecret, err := box.Open(conn.ClientSecretEncrypted)
	if err != nil {
		return nil, false
	}
	refresh, err := box.Open(binding.RefreshTokenEncrypted)
	if err != nil {
		return nil, false
	}
	client, err := newZohoCRMClientFromParts(conn.ClientID, string(clientSecret), string(refresh), conn.Dc)
	if err != nil {
		return nil, false
	}
	return client, true
}
