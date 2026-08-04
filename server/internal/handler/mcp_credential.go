package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/util/secretbox"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Sealed auth for REMOTE MCP servers (dynamic MCP core, docs/decoupling-
// manifest.md Tier 2 #6). A remote MCP entry in an agent's mcp_config is
// `{"type":"http","url":"…","headers":{…}}`; the headers carry a bearer token
// that is a capability (possession exfiltrates whatever the tool exposes). That
// token must NOT sit plaintext in the unsealed `agent.mcp_config` column, so it
// lives here sealed with a secretbox loaded from AGORA_MCP_SECRET_KEY, and is
// merged into the entry's `headers` server-side at task dispatch
// (injectMcpCredentials). Mirrors git_credential / figma_credential exactly:
//   - Writes are admin-only and reject agent actors (a remote MCP with a stolen
//     token exfiltrates data — an agent must never be able to set one).
//   - The status/list endpoint is member-visible but returns has_secret + a
//     last4 hint only, NEVER the token.
//   - If AGORA_MCP_SECRET_KEY is unset the write endpoints fail closed (503)
//     rather than store a token in plaintext.

var (
	mcpBoxOnce sync.Once
	mcpBoxVal  *secretbox.Box
	mcpBoxErr  error
)

func mcpCredentialBox() (*secretbox.Box, error) {
	mcpBoxOnce.Do(func() {
		key, err := secretbox.LoadKey("AGORA_MCP_SECRET_KEY")
		if err != nil {
			mcpBoxErr = err
			return
		}
		mcpBoxVal, mcpBoxErr = secretbox.New(key)
	})
	return mcpBoxVal, mcpBoxErr
}

// mcpSealedAuth is the plaintext shape sealed into mcp_credential.secret_encrypted:
// the full header map merged into a remote entry at dispatch. Sealed as JSON so
// multiple headers ride in the one column and the shape can grow later.
type mcpSealedAuth struct {
	Headers map[string]string `json:"headers"`
}

type mcpCredentialResponse struct {
	ID         string `json:"id"`
	ServerName string `json:"server_name"`
	HasSecret  bool   `json:"has_secret"`
	Last4      string `json:"last4"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// upsertMcpCredentialRequest is the write body. The auth may be given as a
// single header (header_name + secret) — the common bearer-token case — or as a
// full headers map (overrides header_name/secret when non-empty). `secret` /
// header values are write-only and never echoed back.
type upsertMcpCredentialRequest struct {
	ServerName string            `json:"server_name"`
	HeaderName string            `json:"header_name"` // default "Authorization"
	Secret     string            `json:"secret"`      // write-only header value
	Headers    map[string]string `json:"headers"`     // optional full header map
}

// mcpCredentialActorIsAgent rejects agent actors on the write endpoints. A
// remote MCP server with a stolen token exfiltrates data, so only humans
// (owners/admins) may seal one.
func (h *Handler) mcpCredentialActorIsAgent(w http.ResponseWriter, r *http.Request, wsID string) bool {
	if actorType, _ := h.resolveActor(r, requestUserID(r), wsID); actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents may not manage MCP credentials")
		return true
	}
	return false
}

// ListMcpCredentials returns the workspace's MCP credentials WITHOUT any secret
// material (the query never selects secret_encrypted; has_secret is derived).
// Member-visible so the MCP servers panel renders the sealed-auth badge for
// non-admins.
func (h *Handler) ListMcpCredentials(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	rows, err := h.Queries.ListMcpCredentials(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list MCP credentials")
		return
	}
	resp := make([]mcpCredentialResponse, len(rows))
	for i, c := range rows {
		resp[i] = mcpCredentialResponse{
			ID:         uuidToString(c.ID),
			ServerName: c.ServerName,
			HasSecret:  true,
			Last4:      c.SecretLast4,
			CreatedAt:  timestampToString(c.CreatedAt),
			UpdatedAt:  timestampToString(c.UpdatedAt),
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// PutMcpCredential seals (or rotates) the auth for one remote MCP server,
// keyed by server_name. Admin-only + agent-rejected; the token is sealed before
// storage and never echoed back.
func (h *Handler) PutMcpCredential(w http.ResponseWriter, r *http.Request) {
	wsIDStr := workspaceIDFromURL(r, "id")
	wsUUID, ok := parseUUIDOrBadRequest(w, wsIDStr, "workspace id")
	if !ok {
		return
	}
	if h.mcpCredentialActorIsAgent(w, r, wsIDStr) {
		return
	}
	var req upsertMcpCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	serverName := strings.TrimSpace(req.ServerName)
	if serverName == "" {
		writeError(w, http.StatusBadRequest, "server_name is required")
		return
	}

	// Resolve the sealed header map: an explicit headers map wins; otherwise
	// build a single header from header_name (default Authorization) + secret.
	headers := map[string]string{}
	for k, v := range req.Headers {
		if k := strings.TrimSpace(k); k != "" {
			headers[k] = v
		}
	}
	var last4Source string
	if len(headers) == 0 {
		secret := strings.TrimSpace(req.Secret)
		if secret == "" {
			writeError(w, http.StatusBadRequest, "secret (auth header value) is required")
			return
		}
		headerName := strings.TrimSpace(req.HeaderName)
		if headerName == "" {
			headerName = "Authorization"
		}
		headers[headerName] = secret
		last4Source = secret
	} else {
		last4Source = primaryHeaderValue(headers)
	}

	box, err := mcpCredentialBox()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "MCP credentials are not configured on this server (AGORA_MCP_SECRET_KEY unset)")
		return
	}
	plain, err := json.Marshal(mcpSealedAuth{Headers: headers})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode secret")
		return
	}
	sealed, err := box.Seal(plain)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to seal secret")
		return
	}
	creator, ok := parseUUIDOrBadRequest(w, requestUserID(r), "user id")
	if !ok {
		return
	}
	row, err := h.Queries.UpsertMcpCredential(r.Context(), db.UpsertMcpCredentialParams{
		WorkspaceID:     wsUUID,
		ServerName:      serverName,
		SecretEncrypted: sealed,
		SecretLast4:     last4(last4Source),
		CreatedBy:       creator,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save MCP credential")
		return
	}
	writeJSON(w, http.StatusOK, mcpCredentialResponse{
		ID:         uuidToString(row.ID),
		ServerName: row.ServerName,
		HasSecret:  true,
		Last4:      row.SecretLast4,
		CreatedAt:  timestampToString(row.CreatedAt),
		UpdatedAt:  timestampToString(row.UpdatedAt),
	})
}

// DeleteMcpCredential removes the sealed auth for one remote MCP server
// (scoped to the workspace). Admin-only + agent-rejected.
func (h *Handler) DeleteMcpCredential(w http.ResponseWriter, r *http.Request) {
	wsIDStr := workspaceIDFromURL(r, "id")
	wsUUID, ok := parseUUIDOrBadRequest(w, wsIDStr, "workspace id")
	if !ok {
		return
	}
	if h.mcpCredentialActorIsAgent(w, r, wsIDStr) {
		return
	}
	serverName := strings.TrimSpace(chi.URLParam(r, "serverName"))
	if serverName == "" {
		writeError(w, http.StatusBadRequest, "server name is required")
		return
	}
	rows, err := h.Queries.DeleteMcpCredential(r.Context(), db.DeleteMcpCredentialParams{
		WorkspaceID: wsUUID,
		ServerName:  serverName,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete MCP credential")
		return
	}
	if rows == 0 {
		writeError(w, http.StatusNotFound, "MCP credential not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// primaryHeaderValue returns the value of the first header by sorted key, so
// the last4 hint is deterministic for a multi-header credential.
func primaryHeaderValue(headers map[string]string) string {
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return headers[keys[0]]
}

// last4 returns the last 4 characters of s (fewer if shorter). For a value like
// "Bearer abcdef1234" this is "1234" — a UI hint that never reveals the token.
func last4(s string) string {
	r := []rune(s)
	if len(r) <= 4 {
		return string(r)
	}
	return string(r[len(r)-4:])
}

// injectMcpCredentials merges each workspace mcp_credential's sealed headers
// into the matching remote server entry (by name) in the agent's per-task
// mcp_config, so a remote MCP server's bearer token reaches the runtime without
// ever sitting plaintext in agent.mcp_config. Mirrors injectFigmaMcpCreds'
// conservatism: ANY failure (no rows, secret key unset, decrypt error,
// malformed JSON) returns the config unchanged — a claim never fails because of
// MCP credential wiring. Sealed header values win over any placeholder value in
// the config (the sealed store is authoritative for its keys); every other
// server, header, and top-level field is preserved.
func (h *Handler) injectMcpCredentials(ctx context.Context, workspaceID pgtype.UUID, mcpConfig json.RawMessage) json.RawMessage {
	if len(mcpConfig) == 0 {
		return mcpConfig
	}
	creds, err := h.Queries.GetMcpCredentialsForWorkspace(ctx, workspaceID)
	if err != nil || len(creds) == 0 {
		return mcpConfig
	}
	box, err := mcpCredentialBox()
	if err != nil {
		return mcpConfig
	}

	// Decrypt each credential's headers up front; skip any that fail so one bad
	// row can't dark the rest.
	byName := make(map[string]map[string]string, len(creds))
	for _, c := range creds {
		plain, err := box.Open(c.SecretEncrypted)
		if err != nil {
			continue
		}
		var auth mcpSealedAuth
		if err := json.Unmarshal(plain, &auth); err != nil || len(auth.Headers) == 0 {
			continue
		}
		byName[c.ServerName] = auth.Headers
	}
	if len(byName) == 0 {
		return mcpConfig
	}

	out, merged := mergeMcpCredentialHeaders(mcpConfig, byName)
	if !merged {
		return mcpConfig
	}
	slog.Info("mcp credential injection",
		"workspace_id", uuidToString(workspaceID),
		"credentials", len(byName),
	)
	return out
}

// mergeMcpCredentialHeaders is the pure core of injectMcpCredentials: for each
// server whose name has a sealed header set, it merges those headers into the
// entry's `headers` object (sealed values win). Only entries that carry a `url`
// (remote http/sse) are touched — a stdio entry that happens to share a name is
// left alone. Returns (config, merged); malformed input returns the original
// bytes with merged=false.
func mergeMcpCredentialHeaders(mcpConfig json.RawMessage, byName map[string]map[string]string) (json.RawMessage, bool) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(mcpConfig, &root); err != nil || root == nil {
		return mcpConfig, false
	}
	serversRaw, ok := root["mcpServers"]
	if !ok {
		return mcpConfig, false
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(serversRaw, &servers); err != nil || servers == nil {
		return mcpConfig, false
	}

	changed := false
	for name, sealedHeaders := range byName {
		entryRaw, ok := servers[name]
		if !ok {
			continue
		}
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(entryRaw, &entry); err != nil || entry == nil {
			continue
		}
		// Only remote entries (with a url) carry auth headers. Skip a stdio
		// entry that coincidentally matches a credential name.
		urlRaw, hasURL := entry["url"]
		if !hasURL {
			continue
		}
		var urlStr string
		if json.Unmarshal(urlRaw, &urlStr) != nil || strings.TrimSpace(urlStr) == "" {
			continue
		}

		headers := map[string]string{}
		if hRaw, ok := entry["headers"]; ok {
			if err := json.Unmarshal(hRaw, &headers); err != nil || headers == nil {
				headers = map[string]string{}
			}
		}
		for k, v := range sealedHeaders {
			headers[k] = v // sealed value wins
		}
		hBytes, err := json.Marshal(headers)
		if err != nil {
			continue
		}
		entry["headers"] = hBytes
		entryBytes, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		servers[name] = entryBytes
		changed = true
	}
	if !changed {
		return mcpConfig, false
	}

	serversBytes, err := json.Marshal(servers)
	if err != nil {
		return mcpConfig, false
	}
	root["mcpServers"] = serversBytes
	doc, err := json.Marshal(root)
	if err != nil {
		return mcpConfig, false
	}
	return doc, true
}
