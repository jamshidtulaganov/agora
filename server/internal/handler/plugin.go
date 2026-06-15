package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Plugins bundle a set of workspace skills + MCP connectors so an operator can
// install the whole capability onto agents in one action (mirrors the Claude
// plugin model: a plugin groups skills + connectors). Skills are shared
// workspace skills; connectors live in the plugin's mcp_config
// ({"mcpServers": {...}}) and are merged into the target agent's mcp_config on
// install. Implemented with raw pgx (no sqlc) over the plugin / plugin_skill /
// agent_plugin tables (migration 121).

type pluginSkillRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type PluginResponse struct {
	ID                string           `json:"id"`
	Name              string           `json:"name"`
	Description       string           `json:"description"`
	McpConfig         json.RawMessage  `json:"mcp_config,omitempty"`
	Skills            []pluginSkillRef `json:"skills"`
	InstalledAgentIDs []string         `json:"installed_agent_ids"`
}

type CreatePluginRequest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	McpConfig   json.RawMessage `json:"mcp_config"`
	SkillIDs    []string        `json:"skill_ids"`
}

type InstallPluginRequest struct {
	AgentIDs []string `json:"agent_ids"`
}

// redactPluginMcp blanks every env value in an mcp_config blob so connector
// secrets (tokens) are never exposed in list/get responses. Server name,
// command and args stay visible. Returns the original bytes on parse failure
// (best-effort; an unparseable blob is already opaque).
func redactPluginMcp(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return json.RawMessage(raw)
	}
	servers, _ := m["mcpServers"].(map[string]any)
	for _, sv := range servers {
		s, ok := sv.(map[string]any)
		if !ok {
			continue
		}
		if env, ok := s["env"].(map[string]any); ok {
			for k := range env {
				env[k] = "***"
			}
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return out
}

// ListPlugins returns the workspace's plugins with their skills + install state.
func (h *Handler) ListPlugins(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace required")
		return
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT p.id, p.name, p.description, p.mcp_config,
		  COALESCE((SELECT json_agg(json_build_object('id', s.id, 'name', s.name) ORDER BY s.name)
		            FROM plugin_skill ps JOIN skill s ON s.id = ps.skill_id
		            WHERE ps.plugin_id = p.id), '[]') AS skills,
		  COALESCE((SELECT json_agg(ap.agent_id::text)
		            FROM agent_plugin ap WHERE ap.plugin_id = p.id), '[]') AS installed
		FROM plugin p
		WHERE p.workspace_id = $1::uuid
		ORDER BY p.created_at`, wsID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list plugins failed")
		return
	}
	defer rows.Close()
	out := []PluginResponse{}
	for rows.Next() {
		var p PluginResponse
		var mcp []byte
		var skillsJSON, installedJSON []byte
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &mcp, &skillsJSON, &installedJSON); err != nil {
			writeError(w, http.StatusInternalServerError, "scan plugin failed")
			return
		}
		p.McpConfig = redactPluginMcp(mcp)
		_ = json.Unmarshal(skillsJSON, &p.Skills)
		_ = json.Unmarshal(installedJSON, &p.InstalledAgentIDs)
		if p.Skills == nil {
			p.Skills = []pluginSkillRef{}
		}
		if p.InstalledAgentIDs == nil {
			p.InstalledAgentIDs = []string{}
		}
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": out})
}

// CreatePlugin creates a plugin and links its skills.
func (h *Handler) CreatePlugin(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace required")
		return
	}
	var req CreatePluginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	var mcp any
	if len(req.McpConfig) > 0 && string(req.McpConfig) != "null" {
		mcp = []byte(req.McpConfig)
	}
	var id string
	if err := h.DB.QueryRow(r.Context(),
		`INSERT INTO plugin (workspace_id, name, description, mcp_config)
		 VALUES ($1::uuid, $2, $3, $4) RETURNING id::text`,
		wsID, req.Name, req.Description, mcp).Scan(&id); err != nil {
		writeError(w, http.StatusInternalServerError, "create plugin failed")
		return
	}
	for _, sid := range req.SkillIDs {
		if sid == "" {
			continue
		}
		// Only link skills that belong to this workspace.
		if _, err := h.DB.Exec(r.Context(),
			`INSERT INTO plugin_skill (plugin_id, skill_id)
			 SELECT $1::uuid, $2::uuid WHERE EXISTS
			   (SELECT 1 FROM skill WHERE id = $2::uuid AND workspace_id = $3::uuid)
			 ON CONFLICT DO NOTHING`, id, sid, wsID); err != nil {
			writeError(w, http.StatusInternalServerError, "link plugin skill failed")
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// DeletePlugin removes a plugin (cascades plugin_skill + agent_plugin).
func (h *Handler) DeletePlugin(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	id := chi.URLParam(r, "id")
	if _, err := h.DB.Exec(r.Context(),
		`DELETE FROM plugin WHERE id = $1::uuid AND workspace_id = $2::uuid`, id, wsID); err != nil {
		writeError(w, http.StatusInternalServerError, "delete plugin failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// InstallPlugin installs the plugin onto the given agents: binds the plugin's
// skills (agent_skill) and merges its connectors into each agent's mcp_config,
// then records the install (agent_plugin). Idempotent per agent.
func (h *Handler) InstallPlugin(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	pluginID := chi.URLParam(r, "id")
	var req InstallPluginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx := r.Context()

	// Load the plugin (workspace-scoped) + its skills + connectors.
	var pluginMcp []byte
	if err := h.DB.QueryRow(ctx,
		`SELECT mcp_config FROM plugin WHERE id = $1::uuid AND workspace_id = $2::uuid`,
		pluginID, wsID).Scan(&pluginMcp); err != nil {
		writeError(w, http.StatusNotFound, "plugin not found")
		return
	}
	skillRows, err := h.DB.Query(ctx, `SELECT skill_id::text FROM plugin_skill WHERE plugin_id = $1::uuid`, pluginID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load plugin skills failed")
		return
	}
	var skillIDs []string
	for skillRows.Next() {
		var s string
		if err := skillRows.Scan(&s); err == nil {
			skillIDs = append(skillIDs, s)
		}
	}
	skillRows.Close()

	pluginServers := mcpServersOf(pluginMcp)
	installed := 0
	for _, agentID := range req.AgentIDs {
		// Guard: agent must be in this workspace.
		var ok bool
		if err := h.DB.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM agent WHERE id = $1::uuid AND workspace_id = $2::uuid)`,
			agentID, wsID).Scan(&ok); err != nil || !ok {
			continue
		}
		// Bind skills.
		for _, sid := range skillIDs {
			h.DB.Exec(ctx, `INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`, agentID, sid)
		}
		// Merge connectors into the agent's mcp_config.
		if len(pluginServers) > 0 {
			var agentMcp []byte
			h.DB.QueryRow(ctx, `SELECT mcp_config FROM agent WHERE id = $1::uuid`, agentID).Scan(&agentMcp)
			merged := mergeMcpServers(agentMcp, pluginServers)
			h.DB.Exec(ctx, `UPDATE agent SET mcp_config = $2, updated_at = now() WHERE id = $1::uuid`, agentID, merged)
		}
		// Record install.
		h.DB.Exec(ctx, `INSERT INTO agent_plugin (agent_id, plugin_id) VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`, agentID, pluginID)
		installed++
	}
	writeJSON(w, http.StatusOK, map[string]any{"installed": installed})
}

// UninstallPlugin removes the plugin's skills + connectors from the given agents
// and clears the install record.
func (h *Handler) UninstallPlugin(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	pluginID := chi.URLParam(r, "id")
	var req InstallPluginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx := r.Context()
	var pluginMcp []byte
	if err := h.DB.QueryRow(ctx,
		`SELECT mcp_config FROM plugin WHERE id = $1::uuid AND workspace_id = $2::uuid`,
		pluginID, wsID).Scan(&pluginMcp); err != nil {
		writeError(w, http.StatusNotFound, "plugin not found")
		return
	}
	pluginServers := mcpServersOf(pluginMcp)
	for _, agentID := range req.AgentIDs {
		h.DB.Exec(ctx, `DELETE FROM agent_skill WHERE agent_id = $1::uuid AND skill_id IN
			(SELECT skill_id FROM plugin_skill WHERE plugin_id = $2::uuid)`, agentID, pluginID)
		if len(pluginServers) > 0 {
			var agentMcp []byte
			h.DB.QueryRow(ctx, `SELECT mcp_config FROM agent WHERE id = $1::uuid`, agentID).Scan(&agentMcp)
			pruned := removeMcpServers(agentMcp, pluginServers)
			h.DB.Exec(ctx, `UPDATE agent SET mcp_config = $2, updated_at = now() WHERE id = $1::uuid`, agentID, pruned)
		}
		h.DB.Exec(ctx, `DELETE FROM agent_plugin WHERE agent_id = $1::uuid AND plugin_id = $2::uuid`, agentID, pluginID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"uninstalled": len(req.AgentIDs)})
}

// --- mcp_config merge helpers (operate on raw {"mcpServers": {...}} blobs) ---

func mcpServersOf(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	s, _ := m["mcpServers"].(map[string]any)
	return s
}

func mergeMcpServers(agentRaw []byte, add map[string]any) []byte {
	m := map[string]any{}
	if len(agentRaw) > 0 {
		_ = json.Unmarshal(agentRaw, &m)
	}
	servers, _ := m["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	for k, v := range add {
		servers[k] = v
	}
	m["mcpServers"] = servers
	out, _ := json.Marshal(m)
	return out
}

func removeMcpServers(agentRaw []byte, drop map[string]any) []byte {
	if len(agentRaw) == 0 {
		return agentRaw
	}
	m := map[string]any{}
	if err := json.Unmarshal(agentRaw, &m); err != nil {
		return agentRaw
	}
	servers, _ := m["mcpServers"].(map[string]any)
	if servers == nil {
		return agentRaw
	}
	for k := range drop {
		delete(servers, k)
	}
	m["mcpServers"] = servers
	out, _ := json.Marshal(m)
	return out
}
