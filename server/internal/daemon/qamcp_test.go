package daemon

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"
)

// TestInjectQAMcpConfig pins the merge semantics: empty config gains the
// server, existing servers are preserved, an agent's own "agora-qa" entry
// wins, invalid JSON passes through untouched, and the env kill-switch works.
func TestInjectQAMcpConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	servers := func(raw json.RawMessage) map[string]map[string]any {
		t.Helper()
		var doc struct {
			McpServers map[string]map[string]any `json:"mcpServers"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("result does not parse: %v (%s)", err, raw)
		}
		return doc.McpServers
	}

	// Empty/nil config → fresh doc with just agora-qa.
	got := servers(injectQAMcpConfig(nil, logger))
	entry, ok := got[qaMcpServerName]
	if !ok {
		t.Fatalf("agora-qa not injected into empty config: %v", got)
	}
	if entry["command"] == "" || entry["command"] == nil {
		t.Errorf("injected command empty: %v", entry)
	}
	args, _ := entry["args"].([]any)
	if len(args) != 2 || args[0] != "mcp" || args[1] != "qa" {
		t.Errorf("injected args = %v, want [mcp qa]", args)
	}

	// Existing servers preserved alongside the injection; unknown top-level
	// fields survive the round-trip.
	raw := json.RawMessage(`{"mcpServers":{"zoho":{"type":"http","url":"https://x"}},"other":{"keep":true}}`)
	merged := injectQAMcpConfig(raw, logger)
	got = servers(merged)
	if _, ok := got["zoho"]; !ok {
		t.Errorf("existing server dropped: %v", got)
	}
	if _, ok := got[qaMcpServerName]; !ok {
		t.Errorf("agora-qa not merged: %v", got)
	}
	var whole map[string]json.RawMessage
	_ = json.Unmarshal(merged, &whole)
	if _, ok := whole["other"]; !ok {
		t.Errorf("unknown top-level field dropped: %s", merged)
	}

	// The agent's own agora-qa entry wins — config returned unchanged.
	own := json.RawMessage(`{"mcpServers":{"agora-qa":{"command":"/custom/agora","args":["mcp","qa"]}}}`)
	if out := injectQAMcpConfig(own, logger); string(out) != string(own) {
		t.Errorf("agent's own agora-qa entry must win, got %s", out)
	}

	// Invalid JSON passes through untouched (fail-soft, never blocks dispatch).
	bad := json.RawMessage(`{not json`)
	if out := injectQAMcpConfig(bad, logger); string(out) != string(bad) {
		t.Errorf("invalid config must pass through, got %s", out)
	}

	// Kill-switch.
	t.Setenv("AGORA_QA_MCP_DISABLED", "1")
	if out := injectQAMcpConfig(nil, logger); out != nil {
		t.Errorf("disabled: want nil passthrough, got %s", out)
	}
}
