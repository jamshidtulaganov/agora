package handler

import (
	"context"
	"encoding/json"
	"log/slog"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// figmaMcpVersion pins the Framelink figma-developer-mcp release the backend
// auto-provisions. The Dockerfile.daemon preinstall and the UI preset in
// packages/core/mcp/types.ts reference the same version — bump all three
// together.
const figmaMcpVersion = "0.13.2"

// figmaExpiredCredentialNote is appended to the agent's instructions when the
// issue references Figma but the workspace credential is expired/invalid, so
// the agent tells the user instead of failing silently.
const figmaExpiredCredentialNote = "NOTE: this issue references Figma designs, but the workspace Figma credential is expired or invalid — you cannot read them. Tell the user to renew it in Settings → Integrations → Figma instead of guessing at the design."

// figmaMissingCredentialNote is the never-configured sibling of the expired
// note: without it the agent would get ready-made get_figma_data calls for
// tools that were never injected.
const figmaMissingCredentialNote = "NOTE: this issue references Figma designs, but no workspace Figma credential is configured — you cannot read them. Tell the user to add one in Settings → Integrations → Figma instead of guessing at the design."

// figmaCredState is the resolved state of the workspace credential, computed
// by injectFigmaMcpCreds and consumed by the pure applyFigmaMcp.
type figmaCredState string

const (
	figmaCredOK      figmaCredState = "ok"
	figmaCredExpired figmaCredState = "expired"
	figmaCredMissing figmaCredState = "missing" // no row, secret key unset, or decrypt failure
)

// figmaMcpResult reports what applyFigmaMcp did, so the claim path can gate
// the instruction note on the figma tools ACTUALLY being available and emit
// one structured log per claim.
type figmaMcpResult struct {
	Config json.RawMessage
	// Available means the agent will have working figma MCP tools this run:
	// the workspace token was merged/provisioned, or the operator configured
	// their own non-blank key.
	Available bool
	// Note is a claim-time instruction to append when the tools are NOT
	// available but the issue references designs (expired / missing).
	Note        string
	Provisioned bool // entry added by us
	Synthesized bool // whole mcpServers document created from an empty config
	Merged      bool // workspace token filled into an existing entry
}

// injectFigmaMcpCreds fills (or provisions) the `figma` MCP server in the
// per-task mcp_config from the workspace's sealed Figma credential, so any
// agent whose issue references a Figma design can actually open it.
//
// Modeled on injectLarkMcpCreds with one deliberate divergence: when the
// issue carries Figma refs and the agent's config has no "figma" entry, the
// entry is AUTO-PROVISIONED — including synthesizing the entire
// {"mcpServers":{…}} document when the agent has no MCP config at all
// (unlike lark's empty-config short-circuit; most agents have an empty
// config, including the MUL-348 assignee this feature exists for).
//
// Conservative everywhere else:
//   - No credential / secret key unset → config unchanged; a "missing" note
//     when the issue references designs.
//   - Credential expired or probe-flagged → config unchanged + an "expired"
//     note.
//   - "figma" entry present → only a blank FIGMA_API_KEY is filled;
//     operator-set env always wins (a deliberately scoped per-agent token
//     overrides the workspace one). "Blank" includes the empty-string value
//     the UI template stores for the leave-the-key-blank flow.
//   - Auto-provision fires only when the issue actually references Figma.
//   - Any malformed-JSON path (including JSON nulls) returns the input
//     unchanged — a claim never fails because of Figma wiring.
func (h *Handler) injectFigmaMcpCreds(ctx context.Context, agentID string, issue db.Issue, mcpConfig json.RawMessage) figmaMcpResult {
	refs := issueFigmaRefs(issue)
	if len(refs) == 0 && !mcpConfigHasServer(mcpConfig, "figma") {
		// Nothing to provision and nothing to fill — skip the DB hit.
		return figmaMcpResult{Config: mcpConfig}
	}

	token, expired, ok := h.decryptWorkspaceFigmaToken(ctx, issue.WorkspaceID)
	credState := figmaCredOK
	switch {
	case !ok && expired:
		credState = figmaCredExpired
	case !ok:
		credState = figmaCredMissing
	}

	res := applyFigmaMcp(mcpConfig, len(refs) > 0, token, credState)
	slog.Info("figma mcp injection",
		"workspace_id", uuidToString(issue.WorkspaceID),
		"agent_id", agentID,
		"issue_id", uuidToString(issue.ID),
		"cred_state", string(credState),
		"available", res.Available,
		"auto_provisioned", res.Provisioned,
		"synthesized", res.Synthesized,
		"merged", res.Merged,
	)
	return res
}

// applyFigmaMcp is the pure core of the injection: given the raw config, the
// resolved credential, and whether the issue references designs, it returns
// the (possibly rewritten) config plus what happened. No DB, no logging — the
// full decision matrix is unit-tested directly.
func applyFigmaMcp(mcpConfig json.RawMessage, hasRefs bool, token string, credState figmaCredState) figmaMcpResult {
	res := figmaMcpResult{Config: mcpConfig}
	hasEntry := mcpConfigHasServer(mcpConfig, "figma")

	if credState != figmaCredOK {
		// No usable workspace token. The agent may still have working tools
		// if the operator configured their own key on the entry.
		res.Available = hasEntry && figmaEntryHasOperatorKey(mcpConfig)
		if hasRefs && !res.Available {
			if credState == figmaCredExpired {
				res.Note = figmaExpiredCredentialNote
			} else {
				res.Note = figmaMissingCredentialNote
			}
		}
		return res
	}

	if hasEntry {
		out, merged := mergeFigmaMcpEnv(mcpConfig, token)
		res.Config = out
		res.Merged = merged
		// Merged, or the operator already set a non-blank key — either way
		// the tools work. A malformed entry (merge failed, no operator key)
		// leaves the tools dark; fail open with no note rather than lie.
		res.Available = merged || figmaEntryHasOperatorKey(mcpConfig)
		return res
	}

	if !hasRefs {
		return res
	}

	out, provisioned := provisionFigmaMcpServer(mcpConfig, token)
	res.Config = out
	res.Provisioned = provisioned
	res.Synthesized = provisioned && len(mcpConfig) == 0
	res.Available = provisioned
	return res
}

// mcpConfigHasServer reports whether the config declares the named server
// under mcpServers. Malformed input reads as "absent".
func mcpConfigHasServer(mcpConfig json.RawMessage, name string) bool {
	servers, ok := figmaMcpServersOf(mcpConfig)
	if !ok {
		return false
	}
	_, present := servers[name]
	return present
}

// figmaEntryHasOperatorKey reports whether the existing figma entry carries a
// non-empty FIGMA_API_KEY. The empty string counts as blank — it is exactly
// what the UI template stores when the operator follows the documented
// "leave the key blank to use the workspace credential" flow.
func figmaEntryHasOperatorKey(mcpConfig json.RawMessage) bool {
	servers, ok := figmaMcpServersOf(mcpConfig)
	if !ok {
		return false
	}
	env, ok := serverEnvOf(servers["figma"])
	if !ok {
		return false
	}
	return env["FIGMA_API_KEY"] != ""
}

// figmaMcpServersOf unwraps the mcpServers map. Malformed input — including the
// JSON-null shapes json.Unmarshal happily nils a map on — reads as absent.
func figmaMcpServersOf(mcpConfig json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(mcpConfig) == 0 {
		return nil, false
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(mcpConfig, &root); err != nil || root == nil {
		return nil, false
	}
	serversRaw, ok := root["mcpServers"]
	if !ok {
		return nil, false
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(serversRaw, &servers); err != nil || servers == nil {
		return nil, false
	}
	return servers, true
}

// serverEnvOf unwraps one server entry's env map. Missing env reads as an
// empty (valid) map; a malformed entry reads as not-ok.
func serverEnvOf(entryRaw json.RawMessage) (map[string]string, bool) {
	if len(entryRaw) == 0 {
		return nil, false
	}
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(entryRaw, &entry); err != nil || entry == nil {
		return nil, false
	}
	env := map[string]string{}
	if e, ok := entry["env"]; ok {
		if err := json.Unmarshal(e, &env); err != nil || env == nil {
			env = map[string]string{}
		}
	}
	return env, true
}

// figmaMcpServerEntry builds the pinned Framelink server entry. Pure so tests
// can assert the exact provisioned shape.
func figmaMcpServerEntry(token string) map[string]any {
	return map[string]any{
		"command": "npx",
		"args":    []string{"-y", "figma-developer-mcp@" + figmaMcpVersion, "--stdio", "--no-telemetry"},
		"env":     map[string]string{"FIGMA_API_KEY": token},
	}
}

// mergeFigmaMcpEnv fills a blank FIGMA_API_KEY (absent OR empty-string) in an
// existing "figma" server entry. Operator-set non-empty values win; every
// other server, env key, and top-level field is preserved. Pure; malformed
// input (including JSON nulls) returns the original bytes with merged=false.
func mergeFigmaMcpEnv(mcpConfig json.RawMessage, token string) (out json.RawMessage, merged bool) {
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
	figmaRaw, ok := servers["figma"]
	if !ok {
		return mcpConfig, false
	}
	var figmaEntry map[string]json.RawMessage
	if err := json.Unmarshal(figmaRaw, &figmaEntry); err != nil || figmaEntry == nil {
		return mcpConfig, false
	}
	env := map[string]string{}
	if e, ok := figmaEntry["env"]; ok {
		if err := json.Unmarshal(e, &env); err != nil || env == nil {
			env = map[string]string{}
		}
	}
	if env["FIGMA_API_KEY"] != "" {
		return mcpConfig, false
	}
	env["FIGMA_API_KEY"] = token

	envBytes, err := json.Marshal(env)
	if err != nil {
		return mcpConfig, false
	}
	figmaEntry["env"] = envBytes
	figmaBytes, err := json.Marshal(figmaEntry)
	if err != nil {
		return mcpConfig, false
	}
	servers["figma"] = figmaBytes
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

// provisionFigmaMcpServer adds the pinned figma server entry to the config,
// synthesizing the whole {"mcpServers":{…}} document when the config is empty
// and re-initializing JSON-null maps (json.Unmarshal nils a map on a literal
// null without an error — assigning into it would panic the claim endpoint).
// Pure; malformed input returns the original bytes with provisioned=false; an
// existing "figma" entry is never overwritten (callers check first, this is
// defense in depth).
func provisionFigmaMcpServer(mcpConfig json.RawMessage, token string) (out json.RawMessage, provisioned bool) {
	root := map[string]json.RawMessage{}
	if len(mcpConfig) > 0 {
		if err := json.Unmarshal(mcpConfig, &root); err != nil {
			return mcpConfig, false
		}
		if root == nil { // literal `null` config
			root = map[string]json.RawMessage{}
		}
	}
	servers := map[string]json.RawMessage{}
	if serversRaw, ok := root["mcpServers"]; ok {
		if err := json.Unmarshal(serversRaw, &servers); err != nil {
			return mcpConfig, false
		}
		if servers == nil { // "mcpServers": null
			servers = map[string]json.RawMessage{}
		}
	}
	if _, ok := servers["figma"]; ok {
		return mcpConfig, false
	}
	entryBytes, err := json.Marshal(figmaMcpServerEntry(token))
	if err != nil {
		return mcpConfig, false
	}
	servers["figma"] = entryBytes
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
