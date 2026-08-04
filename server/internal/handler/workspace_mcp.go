package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"reflect"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/logger"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// workspaceMcpActivityUpdated is the activity_log `action` constant for
// default-MCP-config changes. Stored on rows where `issue_id IS NULL`.
// Details carry server names only — never server definitions, which hold
// auth material (hosted MCP URLs embed tokens).
const workspaceMcpActivityUpdated = "workspace_default_mcp_updated"

// WorkspaceDefaultMcpConfigResponse is the wire shape for the dedicated
// default-MCP-config endpoints. Kept distinct from `WorkspaceResponse` so
// the secret-bearing config cannot leak into the generic workspace
// resource by accident — same isolation rationale as AgentEnvResponse.
type WorkspaceDefaultMcpConfigResponse struct {
	WorkspaceID string          `json:"workspace_id"`
	McpConfig   json.RawMessage `json:"mcp_config"`
}

// UpdateWorkspaceDefaultMcpConfigRequest is the wire shape for
// `PUT /api/workspaces/{id}/default-mcp-config`. A null / absent
// mcp_config clears the workspace default.
type UpdateWorkspaceDefaultMcpConfigRequest struct {
	McpConfig json.RawMessage `json:"mcp_config"`
}

// authorizeWorkspaceDefaultMcp enforces the auth contract for the
// default-MCP-config endpoints, mirroring authorizeAgentEnv:
//
//  1. The actor MUST resolve to a member (human). The default config is
//     injected into every agent in the workspace at claim time, so an
//     agent that could write it would be able to route every other
//     agent's session through attacker-chosen MCP servers.
//  2. The member must be a workspace owner or admin.
func (h *Handler) authorizeWorkspaceDefaultMcp(w http.ResponseWriter, r *http.Request) (pgtype.UUID, db.Member, bool) {
	id := workspaceIDFromURL(r, "id")
	idUUID, ok := parseUUIDOrBadRequest(w, id, "workspace id")
	if !ok {
		return pgtype.UUID{}, db.Member{}, false
	}

	userID := requestUserID(r)
	actorType, _ := h.resolveActor(r, userID, id)
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents may not access workspace MCP config endpoints")
		return pgtype.UUID{}, db.Member{}, false
	}

	member, ok := h.requireWorkspaceRole(w, r, id, "workspace not found", "owner", "admin")
	if !ok {
		return pgtype.UUID{}, db.Member{}, false
	}

	return idUUID, member, true
}

// GetWorkspaceDefaultMcpConfig returns the workspace's default MCP config
// to owners/admins. Read access follows the agent mcp_config convention
// (owner/admin may read, no reveal audit); writes are audited below.
func (h *Handler) GetWorkspaceDefaultMcpConfig(w http.ResponseWriter, r *http.Request) {
	idUUID, _, ok := h.authorizeWorkspaceDefaultMcp(w, r)
	if !ok {
		return
	}

	raw, err := h.Queries.GetWorkspaceDefaultMcpConfig(r.Context(), idUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workspace MCP config")
		return
	}

	var cfg json.RawMessage
	if len(raw) > 0 {
		cfg = json.RawMessage(raw)
	}
	writeJSON(w, http.StatusOK, WorkspaceDefaultMcpConfigResponse{
		WorkspaceID: uuidToString(idUUID),
		McpConfig:   cfg,
	})
}

// UpdateWorkspaceDefaultMcpConfig sets or clears the workspace default MCP
// config. Persist + audit run inside one transaction (mirrors
// UpdateAgentEnv): an audit-write outage cannot leave an unaudited config
// mutation on disk.
func (h *Handler) UpdateWorkspaceDefaultMcpConfig(w http.ResponseWriter, r *http.Request) {
	idUUID, member, ok := h.authorizeWorkspaceDefaultMcp(w, r)
	if !ok {
		return
	}

	var req UpdateWorkspaceDefaultMcpConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	clearing := len(jsonValueOrNil(req.McpConfig)) == 0
	if !clearing {
		if msg, ok := validateMcpConfigShape(req.McpConfig); !ok {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	}

	oldRaw, err := h.Queries.GetWorkspaceDefaultMcpConfig(r.Context(), idUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workspace MCP config")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		slog.Error("workspace default mcp update: begin tx failed",
			append(logger.RequestAttrs(r), "error", err, "workspace_id", uuidToString(idUUID))...)
		writeError(w, http.StatusInternalServerError, "failed to update workspace MCP config")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	var stored json.RawMessage
	if clearing {
		if _, err := qtx.ClearWorkspaceDefaultMcpConfig(r.Context(), idUUID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update workspace MCP config")
			return
		}
	} else {
		if _, err := qtx.UpdateWorkspaceDefaultMcpConfig(r.Context(), db.UpdateWorkspaceDefaultMcpConfigParams{
			ID:               idUUID,
			DefaultMcpConfig: req.McpConfig,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update workspace MCP config")
			return
		}
		stored = req.McpConfig
	}

	added, removed, changed := diffMcpServerNames(oldRaw, stored)
	details, _ := json.Marshal(map[string]any{
		"workspace_id":    uuidToString(idUUID),
		"added_servers":   added,
		"removed_servers": removed,
		"changed_servers": changed,
		"cleared":         clearing,
	})
	if _, err := qtx.CreateActivity(r.Context(), db.CreateActivityParams{
		WorkspaceID: idUUID,
		IssueID:     pgtype.UUID{}, // config access is not tied to an issue
		ActorType:   pgtype.Text{String: "member", Valid: true},
		ActorID:     parseUUID(uuidToString(member.UserID)),
		Action:      workspaceMcpActivityUpdated,
		Details:     details,
	}); err != nil {
		slog.Error("workspace_default_mcp_updated audit write failed; rolling back update",
			append(logger.RequestAttrs(r), "error", err, "workspace_id", uuidToString(idUUID))...)
		writeError(w, http.StatusInternalServerError, "audit log write failed; MCP config update rolled back")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("workspace default mcp update: tx commit failed",
			append(logger.RequestAttrs(r), "error", err, "workspace_id", uuidToString(idUUID))...)
		writeError(w, http.StatusInternalServerError, "failed to update workspace MCP config")
		return
	}

	slog.Info("workspace default mcp config updated",
		append(logger.RequestAttrs(r), "workspace_id", uuidToString(idUUID), "cleared", clearing)...)
	writeJSON(w, http.StatusOK, WorkspaceDefaultMcpConfigResponse{
		WorkspaceID: uuidToString(idUUID),
		McpConfig:   stored,
	})
}

// applyWorkspaceDefaultMcpServers merges the workspace's default MCP
// servers into an agent's per-task mcp_config at claim time. Agent-level
// entries win on name collision, so a per-agent override of a shared
// server is never clobbered. Any failure returns the agent config
// unchanged — the workspace default must never block a claim.
func (h *Handler) applyWorkspaceDefaultMcpServers(ctx context.Context, workspaceID pgtype.UUID, agentCfg json.RawMessage) json.RawMessage {
	raw, err := h.Queries.GetWorkspaceDefaultMcpConfig(ctx, workspaceID)
	if err != nil {
		slog.Warn("failed to load workspace default mcp config; skipping merge",
			"workspace_id", uuidToString(workspaceID), "error", err)
		return agentCfg
	}
	defaults := mcpServersOf(raw)
	if len(defaults) == 0 {
		return agentCfg
	}

	agentServers := mcpServersOf(agentCfg)
	add := map[string]any{}
	for name, cfg := range defaults {
		if _, exists := agentServers[name]; !exists {
			add[name] = cfg
		}
	}
	if len(add) == 0 {
		return agentCfg
	}
	return json.RawMessage(mergeMcpServers(agentCfg, add))
}

// validateMcpConfigShape enforces the minimal contract every consumer of
// mcp_config assumes: a JSON object whose `mcpServers` value is an object
// mapping non-empty server names to object definitions. Returns a
// user-facing message on failure.
func validateMcpConfigShape(raw json.RawMessage) (string, bool) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "mcp_config must be a JSON object", false
	}
	serversAny, ok := m["mcpServers"]
	if !ok {
		return `mcp_config must contain an "mcpServers" object`, false
	}
	servers, ok := serversAny.(map[string]any)
	if !ok {
		return `"mcpServers" must be an object`, false
	}
	for name, def := range servers {
		if strings.TrimSpace(name) == "" {
			return "mcpServers keys must be non-empty server names", false
		}
		if _, ok := def.(map[string]any); !ok {
			return `mcpServers["` + name + `"] must be an object`, false
		}
	}
	return "", true
}

// diffMcpServerNames summarises which server names an update touched,
// without ever recording server definitions (they carry auth material).
// Slices are sorted so activity rows stay deterministic for tests.
func diffMcpServerNames(oldRaw, newRaw []byte) (added, removed, changed []string) {
	oldServers := mcpServersOf(oldRaw)
	newServers := mcpServersOf(newRaw)
	added, removed, changed = []string{}, []string{}, []string{}

	for name, newDef := range newServers {
		oldDef, ok := oldServers[name]
		if !ok {
			added = append(added, name)
			continue
		}
		if !reflect.DeepEqual(oldDef, newDef) {
			changed = append(changed, name)
		}
	}
	for name := range oldServers {
		if _, ok := newServers[name]; !ok {
			removed = append(removed, name)
		}
	}

	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	return added, removed, changed
}

// jsonValueOrNil treats absent, empty, and literal `null` bodies
// identically so `{"mcp_config": null}` and `{}` both mean "clear".
func jsonValueOrNil(raw json.RawMessage) []byte {
	t := strings.TrimSpace(string(raw))
	if t == "" || t == "null" {
		return nil
	}
	return []byte(t)
}
