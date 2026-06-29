## mcp-injection — source map

This page is a source-traced contract. Every claim maps to a real symbol below. If a symbol moves, is renamed, or changes shape, update `mcp-injection.mdx` **and** this sidecar in the **same PR** (the builtin_skills drift rule).

| File | Symbols this page depends on |
|---|---|
| `packages/core/mcp/types.ts` | `McpConfig`, `McpServerEntry`, `NamedMcpServer`, `readMcpServers`, `listMcpServers`, `upsertMcpServer`, `removeMcpServer`, `buildServerEntry`, `MCP_SERVER_TEMPLATES` |
| `packages/core/agents/mcp-support.ts` | `MCP_SUPPORTED_PROVIDERS` (set: claude, codex, cursor, hermes, kimi, kiro, opencode, openclaw — **codebuddy missing**), `providerSupportsMcpConfig` |
| `server/pkg/agent/claude.go` | `claudeBackend.Execute`, `buildClaudeArgs`, `claudeBlockedArgs` (`--mcp-config` = `blockedWithValue`), `writeMcpConfigToTemp` (`agora-mcp-*.json`), `filterCustomArgs`, launch flags `--mcp-config` + `--strict-mcp-config` |
| `server/pkg/agent/codebuddy.go` | `codebuddyBackend.Execute`, `buildCodebuddyArgs`, `codebuddyBlockedArgs` (`--mcp-config` reserved), launch flags `--mcp-config` + `--strict-mcp-config` — honors MCP but absent from `MCP_SUPPORTED_PROVIDERS` |
| `server/pkg/agent/codex.go` | `ensureCodexMcpConfig`, `renderCodexMcpServersBlock`, `hasManagedCodexMcpConfig` (tri-state predicate), `stripCodexUserMcpServerTables`, `filterCodexCustomConfigOverrides`, `agoraCodexMcpBeginMarker`, `agoraCodexMcpEndMarker`, `codexMcpBlockRe`, `codexBackend.Execute` (fail-closed; requires `CODEX_HOME`; `0o600`) |
| `server/pkg/agent/opencode_mcp.go` | `buildOpenCodeMCPConfigContent`, `translateMCPConfigForOpenCode`, `validateOpenCodeNativeMCPEntry`, `strictDecode` (`DisallowUnknownFields`), `validateOpenCodeOAuth`; injected via env `OPENCODE_CONFIG_CONTENT` (see `opencode.go:107-127`) |
| `server/pkg/agent/hermes.go` | `buildACPMcpServers`, `convertACPMcpServer`, `filterACPMcpServersByCapability`, `extractACPMcpCapabilities`, `buildHermesSessionParams`, `hermesBackend.Execute` (fail-closed on malformed top-level JSON; per-entry skip-with-warning) |
| `server/pkg/agent/kimi.go`, `server/pkg/agent/kiro.go` | both call shared `buildACPMcpServers(opts.McpConfig, …)` |
| `server/internal/handler/agent.go` | `redactMcpConfig` (nulls field, sets `McpConfigRedacted`), agent-actor-never-sees-secrets gate in `ListAgents`/`GetAgent`/mutation path, `canViewAgentSecrets`, `workspaceAlwaysRedactSecrets`, `ClearAgentMcpConfig` + `shouldClearMcpConfig` (tri-state at handler), `AgentResponse.McpConfig`/`McpConfigRedacted` |
| `server/internal/daemon/daemon.go` | copies `task.Agent.McpConfig` into `ExecOptions.McpConfig` / `EnvParams.McpConfig` (~lines 2763-2797, 3008-3067) |
| `server/internal/daemon/execenv/execenv.go` | `EnvParams.McpConfig`, forwarded into `prepareCursorMcpConfig` and reused on env reuse |
| `server/internal/daemon/execenv/cursor_mcp.go` | `prepareCursorMcpConfig`, `hasManagedCursorMcpConfig`, `marshalCursorMcpConfig`, writes `.cursor/mcp.json` + `CURSOR_DATA_DIR` |
| `server/internal/daemon/execenv/openclaw_config.go` | rewrites wrapper config `mcp` block from managed `McpConfig` |

### Key invariants asserted by the page

1. **One column → ExecOptions.McpConfig → two layers.** Backends (Layer 1) read `opts.McpConfig`; execenv preparers (Layer 2: cursor, openclaw) materialise `params.McpConfig` to disk.
2. **Strict mode:** managed config replaces global servers (claude/codebuddy `--strict-mcp-config`; codex `stripCodexUserMcpServerTables`; opencode local-scope last-merge precedence).
3. **Three states:** `null`/NULL = CLI default; `{}` / `{"mcpServers":{}}` = strict empty; populated = strict. Predicate: `hasManagedCodexMcpConfig`; handler: `shouldClearMcpConfig` → `ClearAgentMcpConfig`.
4. **Fail-closed** on malformed config (codex, hermes/kimi/kiro, opencode validation) — never silent global fallback.
5. **Redaction:** agents NEVER see `mcp_config`; `redactMcpConfig` enforces it across all read paths.
6. **Drift bug:** `codebuddy` honors `--mcp-config` but is missing from `MCP_SUPPORTED_PROVIDERS` → tab hidden. Fix = add `"codebuddy"`. Go backends and `mcp-support.ts` are one contract; update together same-PR.
