package daemon

import (
	"encoding/json"
	"log/slog"
	"os"
	"time"
)

// Exported facades over the daemon's test detect/run internals so the QA MCP
// server (internal/qamcp) reuses the exact logic the /editor/test endpoint
// runs — one detection tier list, one runner (login shell, CI=1, ANSI-stripped
// tail, process-group kill), no parallel implementation to drift.

// DetectTestCommand resolves the repo's test command ("" = none configured).
func DetectTestCommand(repoDir string) string { return detectTestCommand(repoDir) }

// RunProjectTests runs command in repoDir and returns (tail output, exit code).
func RunProjectTests(repoDir, command string, timeout time.Duration) (string, int) {
	return runProjectTests(repoDir, command, timeout)
}

// qaMcpServerName is the reserved key the daemon injects into every task's
// mcpServers map. An agent whose own mcp_config already defines this name
// wins — the daemon never overrides an explicit agent entry.
const qaMcpServerName = "agora-qa"

// injectQAMcpConfig merges Agora's own QA MCP server (`agora mcp qa`, served
// by THIS binary) into an agent's mcp_config, so every task — any provider —
// gets the deterministic test-runner tools (detect_tests / run_tests /
// run_case_script / write_test_file) without per-agent setup.
//
// Fail-soft by construction: a raw config that doesn't parse is returned
// unchanged (a broken agent mcp_config must never block dispatch, and the
// daemon must not "fix" JSON it doesn't understand). Opt-out via
// AGORA_QA_MCP_DISABLED=1.
func injectQAMcpConfig(raw json.RawMessage, logger *slog.Logger) json.RawMessage {
	if os.Getenv("AGORA_QA_MCP_DISABLED") == "1" {
		return raw
	}
	exe, err := os.Executable()
	if err != nil {
		logger.Warn("qa mcp: cannot resolve own binary path, skipping injection", "error", err)
		return raw
	}
	entry, err := json.Marshal(map[string]any{
		"command": exe,
		"args":    []string{"mcp", "qa"},
	})
	if err != nil {
		return raw
	}

	// Whole-document map so unknown top-level fields survive the round-trip.
	doc := map[string]json.RawMessage{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			logger.Warn("qa mcp: agent mcp_config does not parse, leaving unchanged", "error", err)
			return raw
		}
	}
	servers := map[string]json.RawMessage{}
	if existing, ok := doc["mcpServers"]; ok && len(existing) > 0 {
		if err := json.Unmarshal(existing, &servers); err != nil {
			logger.Warn("qa mcp: mcpServers does not parse, leaving unchanged", "error", err)
			return raw
		}
	}
	if _, exists := servers[qaMcpServerName]; exists {
		return raw // the agent's own entry wins
	}
	servers[qaMcpServerName] = entry
	merged, err := json.Marshal(servers)
	if err != nil {
		return raw
	}
	doc["mcpServers"] = merged
	out, err := json.Marshal(doc)
	if err != nil {
		return raw
	}
	return out
}
