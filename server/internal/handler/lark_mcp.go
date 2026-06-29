package handler

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/multica-ai/multica/server/internal/integrations/lark"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// injectLarkMcpCreds fills the Lark MCP server's credentials from the agent's
// bound Lark Bot installation, so an agent that scan-installed a Bot can use
// the lark_* tools (groups, messages, docs, calendar) without anyone pasting
// app_id/app_secret by hand.
//
// Why here: app_secret is encrypted at rest and only the backend holds the
// secretbox key (AGORA_LARK_SECRET_KEY). The daemon cannot decrypt, so the
// resolved APP_ID/APP_SECRET/LARK_DOMAIN have to be merged into the per-task
// mcp_config on the server side, just before the claim response is sent.
//
// It is deliberately conservative:
//   - No-op unless the deployment has Lark enabled (h.LarkInstallations != nil).
//   - No-op unless the agent's mcp_config already declares a "lark" server. We
//     never add the server — the operator opts in by configuring the template.
//   - No DB hit until the "lark" server is confirmed present (cheap claims).
//   - No-op unless the agent has an *active* bound installation.
//   - Operator-set env values win: a standalone Lark app (APP_ID/APP_SECRET
//     filled in the template) overrides the bound-Bot creds, matching the
//     template's documented behavior. We only fill blanks.
//
// On any malformed-JSON or decrypt error it returns the input unchanged so a
// claim never fails because of Lark wiring.
func (h *Handler) injectLarkMcpCreds(ctx context.Context, agent db.Agent, mcpConfig json.RawMessage) json.RawMessage {
	if h.LarkInstallations == nil || len(mcpConfig) == 0 {
		return mcpConfig
	}

	// Parse preserving unknown top-level fields so we round-trip the rest of
	// the config untouched.
	var root map[string]json.RawMessage
	if err := json.Unmarshal(mcpConfig, &root); err != nil {
		return mcpConfig
	}
	serversRaw, ok := root["mcpServers"]
	if !ok {
		return mcpConfig
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(serversRaw, &servers); err != nil {
		return mcpConfig
	}
	if _, ok := servers["lark"]; !ok {
		// Agent didn't configure the Lark MCP server — nothing to fill.
		return mcpConfig
	}

	// Confirmed a "lark" server exists; now resolve the bound installation.
	inst, err := h.Queries.GetLarkInstallationByAgent(ctx, db.GetLarkInstallationByAgentParams{
		WorkspaceID: agent.WorkspaceID,
		AgentID:     agent.ID,
	})
	if err != nil || inst.Status != "active" {
		return mcpConfig
	}

	// Decrypt only after we know there's a server to fill and an active
	// installation to fill it from.
	secret, err := h.LarkInstallations.DecryptAppSecret(inst)
	if err != nil {
		slog.Warn("lark mcp: decrypt app_secret failed; leaving creds unfilled",
			"agent_id", uuidToString(agent.ID), "error", err)
		return mcpConfig
	}
	domain := lark.RegionOrDefault(inst.Region).OpenPlatformBaseURL()
	return mergeLarkMcpEnv(mcpConfig, inst.AppID, secret, domain)
}

// mergeLarkMcpEnv merges the resolved Lark credentials into the "lark" server's
// env inside an mcp_config payload. It is pure (no DB / no secretbox) so the
// JSON round-trip is unit-testable on its own.
//
// Operator-set values win: APP_ID/APP_SECRET/LARK_DOMAIN already present in the
// server's env are left untouched (a standalone Lark app overrides the bound
// Bot). Only blanks are filled. Every other server, env key, and top-level
// field is preserved. Any malformed input returns the original bytes unchanged.
func mergeLarkMcpEnv(mcpConfig json.RawMessage, appID, appSecret, domain string) json.RawMessage {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(mcpConfig, &root); err != nil {
		return mcpConfig
	}
	serversRaw, ok := root["mcpServers"]
	if !ok {
		return mcpConfig
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(serversRaw, &servers); err != nil {
		return mcpConfig
	}
	larkRaw, ok := servers["lark"]
	if !ok {
		return mcpConfig
	}
	var larkEntry map[string]json.RawMessage
	if err := json.Unmarshal(larkRaw, &larkEntry); err != nil {
		return mcpConfig
	}
	env := map[string]string{}
	if e, ok := larkEntry["env"]; ok {
		if err := json.Unmarshal(e, &env); err != nil {
			env = map[string]string{}
		}
	}

	if _, ok := env["APP_ID"]; !ok {
		env["APP_ID"] = appID
	}
	if _, ok := env["APP_SECRET"]; !ok {
		env["APP_SECRET"] = appSecret
	}
	if _, ok := env["LARK_DOMAIN"]; !ok {
		env["LARK_DOMAIN"] = domain
	}

	// Re-marshal from the inside out, leaving other servers and fields intact.
	envBytes, err := json.Marshal(env)
	if err != nil {
		return mcpConfig
	}
	larkEntry["env"] = envBytes
	larkBytes, err := json.Marshal(larkEntry)
	if err != nil {
		return mcpConfig
	}
	servers["lark"] = larkBytes
	serversBytes, err := json.Marshal(servers)
	if err != nil {
		return mcpConfig
	}
	root["mcpServers"] = serversBytes
	out, err := json.Marshal(root)
	if err != nil {
		return mcpConfig
	}
	return out
}
