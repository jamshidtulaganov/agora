package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/zohocrm"
	"github.com/jamshidtulaganov/agora/server/internal/util"
)

// Agora-hosted Zoho MCP server (U3, docs/zoho-dynamic-integration.md): a
// Streamable-HTTP MCP endpoint at POST /mcp/zoho that gives agents full Zoho
// tool calling AS THE ACTING USER. The agent's runtime authenticates with
// its task-scoped `mat_` token; the server resolves the acting identity and
// mints Zoho access tokens from sealed credentials entirely server-side —
// no Zoho secret ever reaches a daemon, an agent process, or an mcp_config
// blob (unlike embedded-auth hosted MCP URLs).
//
// Acting identity resolution, most-specific first:
//  1. task initiator's zoho_user_binding (the human who triggered the run)
//  2. runtime owner's zoho_user_binding (X-User-ID from the task token)
//  3. workspace zoho_connection (org-level identity) as fallback
//
// The protocol implementation is a deliberate minimal subset of MCP
// Streamable HTTP (JSON-RPC 2.0 over POST): initialize, ping, tools/list,
// tools/call, plus 202 for client notifications. The server is stateless —
// every request is independently authenticated by the bearer token — so no
// session store is needed; GET (server-initiated streams) answers 405 per
// spec for servers that do not offer them.

var zohoMcpModuleRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,100}$`)

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

// mcpTool is the wire shape of one tool definition for tools/list.
type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func objSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// zohoMcpTools is the static tool surface. Generic + schema-aware by design:
// the modules/fields tools teach the agent the org's real schema (including
// custom modules), and the record tools then operate on any of them — no
// per-module tool definitions.
var zohoMcpTools = []mcpTool{
	{
		Name:        "zoho_whoami",
		Description: "Identity check: which Zoho user (or org-level connection) your calls act as.",
		InputSchema: objSchema(map[string]any{}),
	},
	{
		Name:        "zoho_crm_modules",
		Description: "List the CRM org's modules (stock + custom). Use before touching records to learn what exists.",
		InputSchema: objSchema(map[string]any{}),
	},
	{
		Name:        "zoho_crm_fields",
		Description: "List one module's fields with types and picklist values. Use to learn the schema before searching or writing.",
		InputSchema: objSchema(map[string]any{
			"module": map[string]any{"type": "string", "description": "Module api_name, e.g. Leads or CustomModule34"},
		}, "module"),
	},
	{
		Name:        "zoho_crm_search",
		Description: "Run a COQL SELECT query, e.g. SELECT id, Subject FROM Tasks WHERE Status = 'Open' LIMIT 50. Read-only.",
		InputSchema: objSchema(map[string]any{
			"coql": map[string]any{"type": "string", "description": "A COQL SELECT statement"},
		}, "coql"),
	},
	{
		Name:        "zoho_crm_get_record",
		Description: "Fetch one record by module api_name and record id.",
		InputSchema: objSchema(map[string]any{
			"module": map[string]any{"type": "string"},
			"id":     map[string]any{"type": "string"},
		}, "module", "id"),
	},
	{
		Name:        "zoho_crm_create_record",
		Description: "Create one record. data maps field api_names to values (use zoho_crm_fields to learn them). Attributed to the acting user in Zoho.",
		InputSchema: objSchema(map[string]any{
			"module": map[string]any{"type": "string"},
			"data":   map[string]any{"type": "object", "description": "field api_name → value"},
		}, "module", "data"),
	},
	{
		Name:        "zoho_crm_update_record",
		Description: "Update fields on one record. Attributed to the acting user in Zoho.",
		InputSchema: objSchema(map[string]any{
			"module": map[string]any{"type": "string"},
			"id":     map[string]any{"type": "string"},
			"data":   map[string]any{"type": "object", "description": "field api_name → value"},
		}, "module", "id", "data"),
	},
}

// zohoActingClient resolves the Zoho client for this request's acting
// identity. Returns a human-readable identity label for zoho_whoami.
func (h *Handler) zohoActingClient(ctx context.Context, r *http.Request) (*zohocrm.Client, string, bool) {
	wsUUID, err := util.ParseUUID(r.Header.Get("X-Workspace-ID"))
	if err != nil {
		return nil, "", false
	}

	// 1. Task initiator — the human whose request the agent is serving.
	if taskID, terr := util.ParseUUID(r.Header.Get("X-Task-ID")); terr == nil {
		if task, qerr := h.Queries.GetAgentTask(ctx, taskID); qerr == nil && task.InitiatorUserID.Valid {
			if client, ok := h.zohoCRMClientForUser(ctx, wsUUID, task.InitiatorUserID); ok {
				return client, "task initiator binding", true
			}
		}
	}
	// 2. Runtime owner (the task token's bound user).
	if userID, uerr := util.ParseUUID(r.Header.Get("X-User-ID")); uerr == nil {
		if client, ok := h.zohoCRMClientForUser(ctx, wsUUID, userID); ok {
			return client, "runtime owner binding", true
		}
	}
	// 3. Workspace connection — org-level identity.
	if client, ok := h.zohoCRMClientForWorkspace(ctx, wsUUID); ok {
		return client, "workspace connection (org-level)", true
	}
	return nil, "", false
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
		h.writeZohoMcpResult(w, req.ID, map[string]any{"tools": zohoMcpTools})
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
	args := params.Arguments
	strArg := func(key string) string {
		s, _ := args[key].(string)
		return strings.TrimSpace(s)
	}

	client, identity, ok := h.zohoActingClient(r.Context(), r)
	if !ok {
		h.writeZohoMcpToolResult(w, req.ID, nil, fmt.Errorf(
			"no Zoho identity available: bind your Zoho account in workspace settings, or ask an admin to configure the workspace Zoho connection"))
		return
	}

	requireModule := func() (string, bool) {
		m := strArg("module")
		if !zohoMcpModuleRe.MatchString(m) {
			h.writeZohoMcpToolResult(w, req.ID, nil, fmt.Errorf("module must match %s", zohoMcpModuleRe.String()))
			return "", false
		}
		return m, true
	}

	switch params.Name {
	case "zoho_whoami":
		if user, err := client.GetCurrentUser(r.Context()); err == nil {
			h.writeZohoMcpToolResult(w, req.ID, map[string]any{
				"acting_as": identity, "zoho_user": user,
			}, nil)
			return
		}
		h.writeZohoMcpToolResult(w, req.ID, map[string]any{"acting_as": identity}, nil)
	case "zoho_crm_modules":
		modules, err := client.ListModules(r.Context())
		h.writeZohoMcpToolResult(w, req.ID, map[string]any{"modules": modules}, err)
	case "zoho_crm_fields":
		module, ok := requireModule()
		if !ok {
			return
		}
		fields, err := client.ListFields(r.Context(), module)
		h.writeZohoMcpToolResult(w, req.ID, map[string]any{"module": module, "fields": fields}, err)
	case "zoho_crm_search":
		coql := strArg("coql")
		// COQL is read-only by API design; the SELECT check just fails fast
		// on obviously wrong input. Zoho enforces the acting user's own
		// permissions on every row.
		if !strings.HasPrefix(strings.ToUpper(coql), "SELECT") {
			h.writeZohoMcpToolResult(w, req.ID, nil, fmt.Errorf("coql must be a SELECT statement"))
			return
		}
		rows, more, err := client.Query(r.Context(), coql)
		h.writeZohoMcpToolResult(w, req.ID, map[string]any{"rows": rows, "more_records": more}, err)
	case "zoho_crm_get_record":
		module, ok := requireModule()
		if !ok {
			return
		}
		id := strArg("id")
		if id == "" {
			h.writeZohoMcpToolResult(w, req.ID, nil, fmt.Errorf("id is required"))
			return
		}
		rec, err := client.GetRecord(r.Context(), module, id)
		h.writeZohoMcpToolResult(w, req.ID, rec, err)
	case "zoho_crm_create_record":
		module, ok := requireModule()
		if !ok {
			return
		}
		data, _ := args["data"].(map[string]any)
		if len(data) == 0 {
			h.writeZohoMcpToolResult(w, req.ID, nil, fmt.Errorf("data object is required"))
			return
		}
		id, err := client.CreateRecord(r.Context(), module, data)
		h.writeZohoMcpToolResult(w, req.ID, map[string]any{"id": id, "module": module}, err)
	case "zoho_crm_update_record":
		module, ok := requireModule()
		if !ok {
			return
		}
		id := strArg("id")
		data, _ := args["data"].(map[string]any)
		if id == "" || len(data) == 0 {
			h.writeZohoMcpToolResult(w, req.ID, nil, fmt.Errorf("id and data are required"))
			return
		}
		err := client.UpdateRecord(r.Context(), module, id, data)
		h.writeZohoMcpToolResult(w, req.ID, map[string]any{"id": id, "module": module, "updated": err == nil}, err)
	default:
		writeJSON(w, http.StatusOK, jsonRPCResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &jsonRPCError{Code: -32602, Message: "unknown tool: " + params.Name}})
	}
}

// injectZohoMcpProxy auto-provisions the "zoho" MCP server entry into a
// claimed task's mcp_config when the workspace has a Zoho connection —
// the Figma pattern (provision on relevance) applied at workspace scope.
// The entry's only credential is the task token the daemon already holds;
// an operator-defined "zoho" server in the agent config wins untouched.
func (h *Handler) injectZohoMcpProxy(ctx context.Context, wsUUID pgtype.UUID, mcpConfig json.RawMessage, authToken string) json.RawMessage {
	if authToken == "" || h.cfg.PublicURL == "" {
		return mcpConfig
	}
	if _, err := h.Queries.GetZohoConnectionForWorkspace(ctx, wsUUID); err != nil {
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
