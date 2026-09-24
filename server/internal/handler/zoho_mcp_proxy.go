package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/util"
	"github.com/jamshidtulaganov/agora/server/internal/zohoread"
)

// Agora-hosted Zoho MCP server: a Streamable-HTTP MCP endpoint at POST
// /mcp/zoho that gives agents read-only Zoho CRM and Desk tools (the shared
// zohoread surface). The agent's runtime authenticates with its task-scoped
// `mat_` token; the server resolves the acting person and mints Zoho access
// tokens from their sealed zoho_account grant entirely server-side — no Zoho
// secret ever reaches a daemon, an agent process, or an mcp_config blob.
//
// The acting identity is only ever the person the task works for
// (zohoActingUserForTask, zoho_identity.go) calling with their own Zoho
// grant, so Zoho applies that person's role and sharing rules. There is no
// runtime-owner or org-level fallback. The tools are read-only.
//
// The protocol implementation is a deliberate minimal subset of MCP
// Streamable HTTP (JSON-RPC 2.0 over POST): initialize, ping, tools/list,
// tools/call, plus 202 for client notifications. The server is stateless —
// every request is independently authenticated by the bearer token — so no
// session store is needed; GET (server-initiated streams) answers 405 per
// spec for servers that do not offer them.

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// zohoActingClients resolves the Zoho clients of the person this task works
// for. On failure it returns the message the agent should relay.
func (h *Handler) zohoActingClients(ctx context.Context, r *http.Request) (zohoCall, zohoread.Clients, error) {
	call := zohoCall{Source: "agent"}
	call.WorkspaceID, _ = util.ParseUUID(r.Header.Get("X-Workspace-ID"))
	call.TaskID, _ = util.ParseUUID(r.Header.Get("X-Task-ID"))
	userID, reason, ok := h.zohoTaskIdentityForRequest(ctx, r.Header.Get("X-Task-ID"))
	if !ok {
		return call, zohoread.Clients{}, fmt.Errorf(
			"no Zoho access for this task: Zoho is only read as the person the task works for, and none could be identified. Ask the person to @mention you")
	}
	call.UserID = userID
	clients, ok := h.zohoClientsForUser(ctx, userID)
	if !ok {
		return call, zohoread.Clients{}, fmt.Errorf(
			"no Zoho access for this task: the person who %s hasn't connected their Zoho account in Agora (Settings → Profile → Connected accounts), or it needs reconnecting", reason)
	}
	clients.Me.ActingFor = "the person who " + reason
	return call, clients, nil
}

// ZohoMcpProxy is the Streamable-HTTP MCP endpoint. Auth contract: the
// request must have been authenticated as a task token (the middleware sets
// X-Actor-Source) — the proxy exists for agent runtimes, and a task token is
// the only credential whose blast radius is one task on one workspace.
func (h *Handler) ZohoMcpProxy(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// No server-initiated streams.
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	case http.MethodDelete:
		// Stateless server: session termination is a no-op.
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Header.Get("X-Actor-Source") != "task_token" {
		writeError(w, http.StatusForbidden, "zoho mcp proxy requires a task-scoped token")
		return
	}

	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, jsonRPCResponse{JSONRPC: "2.0", Error: &jsonRPCError{Code: -32700, Message: "parse error"}})
		return
	}

	// Notifications (no id) are acknowledged and dropped.
	if len(req.ID) == 0 || string(req.ID) == "null" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		version := params.ProtocolVersion
		if version == "" {
			version = "2025-03-26"
		}
		h.writeZohoMcpResult(w, req.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "agora-zoho", "version": "1.0.0"},
		})
	case "ping":
		h.writeZohoMcpResult(w, req.ID, map[string]any{})
	case "tools/list":
		h.writeZohoMcpResult(w, req.ID, map[string]any{"tools": zohoread.Tools()})
	case "tools/call":
		h.handleZohoMcpToolCall(w, r, req)
	default:
		writeJSON(w, http.StatusOK, jsonRPCResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &jsonRPCError{Code: -32601, Message: "method not found: " + req.Method}})
	}
}

func (h *Handler) writeZohoMcpResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeJSON(w, http.StatusOK, jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: result})
}

// writeZohoMcpToolResult wraps a tool outcome in the MCP content envelope.
// Tool-level failures are isError results (the agent can read and react),
// NOT protocol errors.
func (h *Handler) writeZohoMcpToolResult(w http.ResponseWriter, id json.RawMessage, payload any, callErr error) {
	if callErr != nil {
		h.writeZohoMcpResult(w, id, map[string]any{
			"content": []map[string]any{{"type": "text", "text": callErr.Error()}},
			"isError": true,
		})
		return
	}
	text, err := json.MarshalIndent(payload, "", " ")
	if err != nil {
		text = []byte(fmt.Sprintf("%v", payload))
	}
	h.writeZohoMcpResult(w, id, map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(text)}},
		"isError": false,
	})
}

func (h *Handler) handleZohoMcpToolCall(w http.ResponseWriter, r *http.Request, req jsonRPCRequest) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSON(w, http.StatusOK, jsonRPCResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &jsonRPCError{Code: -32602, Message: "invalid params"}})
		return
	}
	known := false
	for _, t := range zohoread.Tools() {
		if t.Name == params.Name {
			known = true
			break
		}
	}
	if !known {
		writeJSON(w, http.StatusOK, jsonRPCResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &jsonRPCError{Code: -32602, Message: "unknown tool: " + params.Name}})
		return
	}
	call, clients, err := h.zohoActingClients(r.Context(), r)
	if err != nil {
		h.writeZohoMcpToolResult(w, req.ID, nil, err)
		return
	}
	res, err := h.runZohoTool(r.Context(), call, clients, params.Name, params.Arguments)
	h.writeZohoMcpToolResult(w, req.ID, res.Value, err)
}

// injectZohoMcpProxy auto-provisions the "zoho" MCP server entry into a
// claimed task's mcp_config when the person the task works for has a
// connected Zoho account — the Figma pattern (provision on relevance). The
// entry's only credential is the task token the daemon already holds; an
// operator-defined "zoho" server in the agent config wins untouched.
func (h *Handler) injectZohoMcpProxy(ctx context.Context, taskID pgtype.UUID, mcpConfig json.RawMessage, authToken string) json.RawMessage {
	if authToken == "" || h.cfg.PublicURL == "" {
		return mcpConfig
	}
	userID, _, ok := h.zohoActingUserForTask(ctx, taskID)
	if !ok {
		return mcpConfig
	}
	if acc, err := h.Queries.GetZohoAccountByUser(ctx, userID); err != nil || acc.Status != "connected" {
		return mcpConfig
	}
	servers := mcpServersOf(mcpConfig)
	if _, exists := servers["zoho"]; exists {
		return mcpConfig
	}
	entry := map[string]any{
		"type": "http",
		"url":  strings.TrimRight(h.cfg.PublicURL, "/") + "/mcp/zoho",
		"headers": map[string]any{
			"Authorization": "Bearer " + authToken,
		},
	}
	return json.RawMessage(mergeMcpServers(mcpConfig, map[string]any{"zoho": entry}))
}
