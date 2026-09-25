package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/zohocrm"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/zohodesk"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	"github.com/jamshidtulaganov/agora/server/internal/zohoread"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// A person's own Zoho account (docs/workspace-knowledge-plan.md, Part B).
// Each person connects once through Zoho's consent screen; the grant carries
// read scopes only, is sealed at rest with AGORA_ZOHO_SECRET_KEY, and is used
// for every Zoho read made for that person — so their own Zoho role, profile
// and sharing rules decide what the Assistant and their agents can see.
//
// The OAuth client is the workspace's Zoho connector: an owner/admin sets it
// up in Settings → Integrations → Zoho (zoho_connection) with a server-based
// client registered in api-console.zoho.com, whose redirect URI is
// <AGORA_PUBLIC_URL>/api/integrations/zoho/callback. The server needs only
// AGORA_ZOHO_SECRET_KEY and AGORA_PUBLIC_URL. ZOHO_DYN_ACCOUNTS_BASE /
// ZOHO_DYN_API_BASE / ZOHO_DYN_DESK_BASE point every Zoho host at a
// stand-in (tests, `go run ./cmd/fakezoho`).

// zohoScopes is read-only by construction: with these scopes the token
// itself cannot create or change anything in Zoho.
const zohoScopes = "ZohoCRM.modules.READ,ZohoCRM.coql.READ,ZohoCRM.settings.READ,ZohoCRM.users.READ," +
	"Desk.tickets.READ,Desk.basic.READ,Desk.contacts.READ,Desk.search.READ,Desk.settings.READ"

// zohoHostOverrides are the test / local-fake host overrides; empty in
// production, where hosts derive from the data center.
type zohoHostOverrides struct {
	Accounts string
	API      string
	Desk     string
}

func zohoHosts() zohoHostOverrides {
	return zohoHostOverrides{
		Accounts: os.Getenv("ZOHO_DYN_ACCOUNTS_BASE"),
		API:      os.Getenv("ZOHO_DYN_API_BASE"),
		Desk:     os.Getenv("ZOHO_DYN_DESK_BASE"),
	}
}

// zohoRedirectURI is where Zoho sends the browser back after consent — the
// URI an admin registers for the connector's client. Empty when the server
// doesn't know its public URL (then nobody can connect).
func (h *Handler) zohoRedirectURI() string {
	if h.cfg.PublicURL == "" {
		return ""
	}
	return strings.TrimRight(h.cfg.PublicURL, "/") + "/api/integrations/zoho/callback"
}

// zohoConnector is a workspace connector's OAuth client, decrypted.
type zohoConnector struct {
	ID           pgtype.UUID
	DC           string
	ClientID     string
	ClientSecret string
}

func zohoConnectorFromRow(row db.ZohoConnection) (zohoConnector, bool) {
	box, err := zohoConnectionBox()
	if err != nil {
		return zohoConnector{}, false
	}
	secret, err := box.Open(row.ClientSecretEncrypted)
	if err != nil {
		return zohoConnector{}, false
	}
	return zohoConnector{ID: row.ID, DC: row.Dc, ClientID: row.ClientID, ClientSecret: string(secret)}, true
}

// zohoWorkspaceConnector is the connector people connect through from this
// workspace. ok=false when the workspace has none, or the server can't take
// connections (no public URL for the redirect, no sealing key).
func (h *Handler) zohoWorkspaceConnector(ctx context.Context, wsUUID pgtype.UUID) (zohoConnector, bool) {
	if h.zohoRedirectURI() == "" {
		return zohoConnector{}, false
	}
	row, err := h.Queries.GetZohoConnectionForWorkspace(ctx, wsUUID)
	if err != nil {
		return zohoConnector{}, false
	}
	return zohoConnectorFromRow(row)
}

// zohoConnectorByID is the connector a person's grant was minted under.
func (h *Handler) zohoConnectorByID(ctx context.Context, id pgtype.UUID) (zohoConnector, bool) {
	if !id.Valid {
		return zohoConnector{}, false
	}
	row, err := h.Queries.GetZohoConnectionByID(ctx, id)
	if err != nil {
		return zohoConnector{}, false
	}
	return zohoConnectorFromRow(row)
}

type zohoAccountResponse struct {
	Available       bool     `json:"available"`
	Connected       bool     `json:"connected"`
	Status          string   `json:"status,omitempty"`
	Email           string   `json:"email,omitempty"`
	Name            string   `json:"name,omitempty"`
	CRMRole         string   `json:"crm_role,omitempty"`
	CRMProfile      string   `json:"crm_profile,omitempty"`
	DeskDepartments []string `json:"desk_departments,omitempty"`
	CheckedAt       string   `json:"checked_at,omitempty"`
}

func deskDepartmentNames(raw []byte) []string {
	var names []string
	_ = json.Unmarshal(raw, &names)
	return names
}

func zohoAccountResponseFromRow(available bool, acc db.ZohoAccount) zohoAccountResponse {
	status := acc.Status
	if !acc.ConnectionID.Valid {
		// The connector it was minted under was removed.
		status = "reconnect"
	}
	resp := zohoAccountResponse{
		Available:       available,
		Connected:       true,
		Status:          status,
		Email:           acc.ZohoEmail,
		Name:            acc.ZohoName,
		CRMRole:         acc.CrmRole,
		CRMProfile:      acc.CrmProfile,
		DeskDepartments: deskDepartmentNames(acc.DeskDepartments),
	}
	if acc.CheckedAt.Valid {
		resp.CheckedAt = acc.CheckedAt.Time.UTC().Format(time.RFC3339)
	}
	return resp
}

// zohoAccountCaller is the signed-in person for the /api/me/zoho endpoints.
// Agents are refused: an agent must never be able to re-point its person's
// Zoho identity.
func (h *Handler) zohoAccountCaller(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	if r.Header.Get("X-Actor-Source") == "task_token" {
		writeError(w, http.StatusForbidden, "agents can't manage Zoho accounts")
		return pgtype.UUID{}, false
	}
	return parseUUIDOrBadRequest(w, requestUserID(r), "user id")
}

// GetMyZohoAccount — GET /api/me/zoho.
func (h *Handler) GetMyZohoAccount(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.zohoAccountCaller(w, r)
	if !ok {
		return
	}
	// available: this workspace has a connector the person can connect
	// through (and they belong to it).
	available := false
	if wsUUID, err := util.ParseUUID(r.URL.Query().Get("workspace_id")); err == nil && h.isZohoWorkspaceMember(r.Context(), wsUUID, userID) {
		_, available = h.zohoWorkspaceConnector(r.Context(), wsUUID)
	}
	acc, err := h.Queries.GetZohoAccountByUser(r.Context(), userID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, zohoAccountResponse{Available: available})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load Zoho account")
		return
	}
	writeJSON(w, http.StatusOK, zohoAccountResponseFromRow(available, acc))
}

// ConnectMyZohoAccount — POST /api/me/zoho/connect {workspace_id}. Returns
// the Zoho consent URL for the workspace's connector; the browser goes there
// and Zoho sends it back to the callback.
func (h *Handler) ConnectMyZohoAccount(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.zohoAccountCaller(w, r)
	if !ok {
		return
	}
	var req struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(req.WorkspaceID), "workspace_id")
	if !ok {
		return
	}
	if !h.isZohoWorkspaceMember(r.Context(), wsUUID, userID) {
		writeError(w, http.StatusForbidden, "you are not a member of that workspace")
		return
	}
	conn, ok := h.zohoWorkspaceConnector(r.Context(), wsUUID)
	if !ok {
		writeError(w, http.StatusConflict, "This workspace's Zoho connector isn't set up yet. Ask a workspace owner or admin to set it up in Settings → Integrations → Zoho.")
		return
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start Zoho connection")
		return
	}
	state := hex.EncodeToString(buf)
	if err := h.Queries.PruneZohoOAuthStates(r.Context()); err != nil {
		slog.Warn("zoho: prune oauth states", "error", err)
	}
	if err := h.Queries.CreateZohoOAuthState(r.Context(), db.CreateZohoOAuthStateParams{
		State: state, UserID: userID, ConnectionID: conn.ID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start Zoho connection")
		return
	}
	// The connector's home DC hosts the consent screen; with multi-DC enabled
	// on the client it serves people in every DC, and the callback learns the
	// person's DC from `location`.
	url, err := zohocrm.AuthorizeURL(conn.DC, zohoHosts().Accounts, conn.ClientID, h.zohoRedirectURI(), zohoScopes, state)
	if err != nil {
		slog.Error("zoho: build consent url", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to start Zoho connection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// isZohoWorkspaceMember reports whether userID belongs to wsUUID.
func (h *Handler) isZohoWorkspaceMember(ctx context.Context, wsUUID, userID pgtype.UUID) bool {
	_, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: userID, WorkspaceID: wsUUID})
	return err == nil
}

// DisconnectMyZohoAccount — DELETE /api/me/zoho. Removes the account and
// revokes the grant at Zoho (best effort: the local copy is gone either way).
func (h *Handler) DisconnectMyZohoAccount(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.zohoAccountCaller(w, r)
	if !ok {
		return
	}
	acc, err := h.Queries.DeleteZohoAccountByUser(r.Context(), userID)
	zohoClientCache.Delete(uuidToString(userID))
	if errors.Is(err, pgx.ErrNoRows) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to disconnect Zoho")
		return
	}
	if box, berr := zohoConnectionBox(); berr == nil {
		if refresh, oerr := box.Open(acc.RefreshTokenEncrypted); oerr == nil {
			if rerr := zohocrm.RevokeToken(r.Context(), acc.Dc, zohoHosts().Accounts, string(refresh)); rerr != nil {
				slog.Warn("zoho: revoke on disconnect", "user_id", uuidToString(userID), "error", rerr)
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// ZohoOAuthCallback — GET /api/integrations/zoho/callback (public: the
// browser arrives here from Zoho). The single-use state ties the grant to the
// person who clicked Connect; the result is a small page telling them to go
// back to Agora.
func (h *Handler) ZohoOAuthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("error") != "" {
		zohoCallbackPage(w, http.StatusOK, "Zoho wasn't connected", "The connection was cancelled in Zoho. You can close this tab and try again from Agora.")
		return
	}
	hosts := zohoHosts()
	code, state := q.Get("code"), q.Get("state")
	if code == "" || state == "" {
		zohoCallbackPage(w, http.StatusBadRequest, "Zoho wasn't connected", "This link is incomplete. Go back to Agora and click Connect Zoho again.")
		return
	}
	pending, err := h.Queries.ConsumeZohoOAuthState(r.Context(), state)
	if err != nil {
		zohoCallbackPage(w, http.StatusBadRequest, "This link has expired", "Go back to Agora and click Connect Zoho again.")
		return
	}
	userID := pending.UserID
	conn, ok := h.zohoConnectorByID(r.Context(), pending.ConnectionID)
	if !ok || h.zohoRedirectURI() == "" {
		zohoCallbackPage(w, http.StatusConflict, "Zoho wasn't connected", "The workspace's Zoho connector was removed or changed. Go back to Agora and try again.")
		return
	}
	dc, ok := zohocrm.DCFromLocation(q.Get("location"))
	if !ok {
		zohoCallbackPage(w, http.StatusBadRequest, "Zoho wasn't connected", "Your Zoho account is in a data center this server doesn't support.")
		return
	}
	// Never trust accounts-server blindly: without a test override it must be
	// the known accounts host for that DC.
	if hosts.Accounts == "" {
		if as := strings.TrimRight(q.Get("accounts-server"), "/"); as != "" && as != zohocrm.DCHosts[dc].Accounts {
			zohoCallbackPage(w, http.StatusBadRequest, "Zoho wasn't connected", "Zoho sent back an unexpected sign-in server.")
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	grant, err := zohocrm.ExchangeCode(ctx, conn.ClientID, conn.ClientSecret, code, h.zohoRedirectURI(), dc, hosts.Accounts)
	if err != nil {
		slog.Warn("zoho: code exchange", "user_id", uuidToString(userID), "error", err)
		zohoCallbackPage(w, http.StatusBadGateway, "Zoho wasn't connected", "Zoho didn't accept the sign-in. Go back to Agora and try again.")
		return
	}
	crm, err := zohocrm.New(conn.ClientID, conn.ClientSecret, grant.RefreshToken, dc, hosts.Accounts, hosts.API)
	if err != nil {
		zohoCallbackPage(w, http.StatusInternalServerError, "Zoho wasn't connected", "Something went wrong on our side. Try again.")
		return
	}

	params := db.UpsertZohoAccountParams{UserID: userID, ConnectionID: conn.ID, Dc: dc, Scopes: grant.Scope, DeskDepartments: []byte("[]")}
	crmOK := false
	if me, err := crm.GetCurrentUser(ctx); err == nil {
		crmOK = true
		params.ZohoEmail, params.ZohoName = me.Email, me.FullName
		params.CrmUserID, params.CrmRole, params.CrmProfile = me.ID, me.Role.Name, me.Profile.Name
	} else {
		slog.Info("zoho: CRM not available for account", "user_id", uuidToString(userID), "error", err)
	}
	deskOK := false
	if desk, err := zohodesk.New(crm, dc, hosts.Desk, ""); err == nil {
		if orgs, err := desk.ListOrganizations(ctx); err == nil && len(orgs) > 0 {
			desk = desk.WithOrg(string(orgs[0].ID))
			if agent, err := desk.MyInfo(ctx); err == nil {
				deskOK = true
				params.DeskOrgID, params.DeskAgentID = desk.OrgID(), string(agent.ID)
				if params.ZohoEmail == "" {
					params.ZohoEmail, params.ZohoName = agent.EmailID, agent.DisplayName()
				}
				params.DeskDepartments = deskDepartmentsJSON(ctx, desk, agent)
			}
		} else if err != nil {
			slog.Info("zoho: Desk not available for account", "user_id", uuidToString(userID), "error", err)
		}
	}
	if !crmOK && !deskOK {
		zohoCallbackPage(w, http.StatusBadGateway, "Zoho wasn't connected", "Agora couldn't read Zoho CRM or Zoho Desk with this account.")
		return
	}

	box, err := zohoConnectionBox()
	if err != nil {
		zohoCallbackPage(w, http.StatusServiceUnavailable, "Zoho isn't set up", "This Agora server isn't set up to store Zoho connections.")
		return
	}
	sealed, err := box.Seal([]byte(grant.RefreshToken))
	if err != nil {
		zohoCallbackPage(w, http.StatusInternalServerError, "Zoho wasn't connected", "Something went wrong on our side. Try again.")
		return
	}
	params.RefreshTokenEncrypted = sealed
	if _, err := h.Queries.UpsertZohoAccount(r.Context(), params); err != nil {
		zohoCallbackPage(w, http.StatusInternalServerError, "Zoho wasn't connected", "Something went wrong on our side. Try again.")
		return
	}
	zohoClientCache.Delete(uuidToString(userID))
	who := params.ZohoEmail
	if who == "" {
		who = "your Zoho account"
	}
	zohoCallbackPage(w, http.StatusOK, "Zoho is connected", "Connected as "+who+". You can close this tab and go back to Agora.")
}

// deskDepartmentsJSON names the departments the agent belongs to (or, when
// Desk doesn't list them, every department they can see).
func deskDepartmentsJSON(ctx context.Context, desk *zohodesk.Client, agent zohodesk.Agent) []byte {
	deps, err := desk.ListDepartments(ctx)
	if err != nil {
		return []byte("[]")
	}
	mine := map[string]bool{}
	for _, id := range agent.AssociatedDepartmentIDs {
		mine[string(id)] = true
	}
	names := []string{}
	for _, d := range deps {
		if len(mine) == 0 || mine[string(d.ID)] {
			names = append(names, d.Name)
		}
	}
	raw, _ := json.Marshal(names)
	return raw
}

func zohoCallbackPage(w http.ResponseWriter, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<!doctype html><html lang="en"><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>` + html.EscapeString(title) + ` · Agora</title>` +
		`<body style="font-family:system-ui,sans-serif;max-width:30rem;margin:18vh auto;padding:0 1.25rem;line-height:1.55;color:#18181b">` +
		`<h1 style="font-size:1.35rem;margin:0 0 .5rem">` + html.EscapeString(title) + `</h1>` +
		`<p style="margin:0;color:#52525b">` + html.EscapeString(body) + `</p></body></html>`))
}

// zohoClientCache keeps one person's live Zoho clients (and so their cached
// access token) across calls — Zoho caps how often a grant may mint tokens.
// Entries are keyed by user id and invalidated when the account row changes.
var zohoClientCache sync.Map

type zohoCachedClients struct {
	updatedAt time.Time
	clients   zohoread.Clients
}

// zohoClientsForUser builds (or reuses) the person's Zoho clients. ok=false
// when they haven't connected Zoho, the connection needs a reconnect, or the
// server can't offer Zoho.
func (h *Handler) zohoClientsForUser(ctx context.Context, userID pgtype.UUID) (zohoread.Clients, bool) {
	acc, err := h.Queries.GetZohoAccountByUser(ctx, userID)
	if err != nil || acc.Status != "connected" {
		return zohoread.Clients{}, false
	}
	key := uuidToString(userID)
	if v, ok := zohoClientCache.Load(key); ok {
		if c := v.(zohoCachedClients); c.updatedAt.Equal(acc.UpdatedAt.Time) {
			return c.clients, true
		}
	}
	conn, ok := h.zohoConnectorByID(ctx, acc.ConnectionID)
	if !ok {
		return zohoread.Clients{}, false
	}
	box, err := zohoConnectionBox()
	if err != nil {
		return zohoread.Clients{}, false
	}
	refresh, err := box.Open(acc.RefreshTokenEncrypted)
	if err != nil {
		return zohoread.Clients{}, false
	}
	hosts := zohoHosts()
	crm, err := zohocrm.New(conn.ClientID, conn.ClientSecret, string(refresh), acc.Dc, hosts.Accounts, hosts.API)
	if err != nil {
		return zohoread.Clients{}, false
	}
	clients := zohoread.Clients{Me: zohoread.Identity{
		Email: acc.ZohoEmail, Name: acc.ZohoName, CRMRole: acc.CrmRole, CRMProfile: acc.CrmProfile,
		DeskAgentID: acc.DeskAgentID, DeskDepartments: deskDepartmentNames(acc.DeskDepartments),
	}}
	if acc.CrmUserID != "" {
		clients.CRM = crm
	}
	if acc.DeskOrgID != "" {
		if desk, err := zohodesk.New(crm, acc.Dc, hosts.Desk, acc.DeskOrgID); err == nil {
			clients.Desk = desk
		}
	}
	zohoClientCache.Store(key, zohoCachedClients{updatedAt: acc.UpdatedAt.Time, clients: clients})
	return clients, true
}

// zohoCall is one Zoho read for the audit log.
type zohoCall struct {
	UserID      pgtype.UUID
	WorkspaceID pgtype.UUID
	TaskID      pgtype.UUID
	Source      string // "agent" | "assistant"
}

// runZohoTool runs one read-only tool as the person and records it. A grant
// Zoho rejects flips the account to "reconnect" so the UI can say so.
func (h *Handler) runZohoTool(ctx context.Context, call zohoCall, clients zohoread.Clients, name string, args map[string]any) (zohoread.Result, error) {
	start := time.Now()
	res, err := zohoread.Call(ctx, clients, name, args)
	if err != nil && zohocrm.IsAuthError(err) {
		if merr := h.Queries.MarkZohoAccountReconnect(ctx, call.UserID); merr != nil {
			slog.Warn("zoho: mark reconnect", "error", merr)
		}
		zohoClientCache.Delete(uuidToString(call.UserID))
		err = errors.New("Zoho no longer accepts this person's connection; they need to reconnect Zoho in Agora")
	}
	errText := ""
	if err != nil {
		errText = err.Error()
		if len(errText) > 500 {
			errText = errText[:500]
		}
	}
	if lerr := h.Queries.InsertZohoCallLog(ctx, db.InsertZohoCallLogParams{
		UserID:      call.UserID,
		WorkspaceID: call.WorkspaceID,
		Source:      call.Source,
		TaskID:      call.TaskID,
		Tool:        name,
		Object:      res.Object,
		RecordCount: int32(res.Count),
		DurationMs:  int32(time.Since(start).Milliseconds()),
		Error:       errText,
	}); lerr != nil {
		slog.Warn("zoho: call log", "error", lerr)
	}
	return res, err
}
