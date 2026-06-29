# Source map: protocol-families.mdx

This sidecar traces every load-bearing claim on `/developers/agentic/protocol-families`
back to the exact symbol in `server/pkg/agent/`. Keep it in sync with the MDX page in the
same PR (the same-PR drift rule). Line numbers are approximate and for navigation only;
the symbol name is the contract.

## Family taxonomy

| Claim on page | Source |
|---|---|
| 13 backends → 4 families | `server/pkg/agent/` package: one `*Backend` struct per CLI; transports grouped by the four `Execute` shapes below |
| Every `Execute` returns `*Session{ Messages, Result }` | `claude.go` `claudeBackend.Execute` return; mirrored in `gemini.go`, `opencode.go`, `openclaw.go`, `codex.go`, `hermes.go` |

## Family 1 — bidirectional stream-json (claude/codebuddy/cursor)

| Claim | Source (claude.go) |
|---|---|
| Hardcoded arg prefix `-p --output-format stream-json --input-format stream-json --verbose --strict-mcp-config --permission-mode bypassPermissions --disallowedTools AskUserQuestion` | `buildClaudeArgs` (~L500-515) |
| `--disallowedTools AskUserQuestion` rationale (#2588) | comment in `buildClaudeArgs` (~L508-514) |
| Optional `--model` / `--effort` / `--max-turns` / `--append-system-prompt` / `--resume` | `buildClaudeArgs` (~L516-534) |
| Blocked args (`-p`, `--output-format`, `--input-format`, `--permission-mode`, `--mcp-config`, `--effort`) | `claudeBlockedArgs` (~L485-498) |
| Prompt as JSON `user` frame, trailing newline | `buildClaudeInput` (~L551-569) |
| Prompt write on its own goroutine | `go func(){ writeClaudeInput(stdin, prompt) }` (~L117-123) |
| Deadlock fix narrative ("write \|1: The pipe has been ended.") | block comment above the writer goroutine (~L102-115) |
| stdin kept open for `control_request`; `closeStdin` + `sync.Once` | `closeStdinOnce` / `closeStdin` (~L78-79); closed on `result`, ctx cancel, end of scan |
| SDK message types switch (`system`/`assistant`/`user`/`result`/`log`/`control_request`) | scanner loop switch on `msg.Type` (~L161-195) |
| assistant block handling (text/thinking/tool_use) + per-model usage | `handleAssistant` (~L258-298) |
| user `tool_result` → `MessageToolResult` | `handleUser` (~L300-319) |
| control_response `behavior:"allow"` + `updatedInput` echo | `handleControlRequest` (~L321-357) |
| MCP via temp file + `--mcp-config`, `--strict-mcp-config` strictness | `writeMcpConfigToTemp` (~L728-743); append at ~L50; temp ownership transfer at ~L96-97 |

## Family 2 — one-shot stream-json flag (gemini)

| Claim | Source (gemini.go) |
|---|---|
| Hardcoded `-p <prompt> --yolo -o stream-json` | `buildGeminiArgs` (~L250-264) |
| Blocked args (`-p`, `--yolo`, `-o`) | `geminiBlockedArgs` (~L244-248) |
| Optional `-m <model>` / `-r <session>` | `buildGeminiArgs` (~L256-261) |
| No stdin protocol (one-shot) | `geminiBackend.Execute` opens only `StdoutPipe` (~L43-47) |
| Event types `init`/`message`/`tool_use`/`tool_result`/`error`/`result` | scanner switch on `evt.Type` (~L92-136) |
| Per-model usage from `stats` | `accumulateUsage` (~L169-177) |
| `GEMINI_CLI_TRUST_WORKSPACE=true` default, exit code 55 (#2516) | `buildGeminiEnv` (~L279-290) |

## Family 3 — one-shot JSON (opencode/openclaw/pi/copilot/antigravity)

| Claim | Source |
|---|---|
| `run --format json --dangerously-skip-permissions` prefix | opencode.go `Execute` (~L52) |
| Appended `--dir`/`--model`/`--variant`/`--prompt`/`--session` + trailing positional prompt | opencode.go `Execute` (~L62-81) |
| Blocked args (`--format`, `--dir`, `--variant`, `--dangerously-skip-permissions`) | `opencodeBlockedArgs` (~L19-24) |
| Event types text/tool_use/error/step_start/step_finish; error event (not RC) is failure signal | `processEvents` (~L242-261), `handleErrorEvent` (~L320-337) |
| `--max-turns` unsupported, logged-and-ignored | opencode.go `Execute` (~L74-76) |
| Windows `.cmd` shim → native binary swap | `resolveOpenCodeNativeFromShim` (~L362-374) |
| MCP via `OPENCODE_CONFIG_CONTENT` env | opencode.go (~L118-128) `buildOpenCodeMCPConfigContent` |
| openclaw argv `agent --local --json --session-id ... --message <prompt>` | `buildOpenclawArgs` (~L187-215) |
| `--local` dropped only in gateway mode (#3260); stays blocked | `buildOpenclawArgs` (~L189-191); `openclawBlockedArgs` (~L39-46) |
| openclaw rejects `--model`/`--system-prompt`; model via `--agent`, prompt prepend | `openclawBlockedArgs` (~L44-45); `buildOpenclawArgs` (~L205-213) |
| `minOpenclawVersion` 2026.5.5 fail-fast (PR #2101) | `checkOpenclawVersion` / `minOpenclawVersion` (~L30, L234-249) |
| whole-buffer parse first, then NDJSON fallback | `processOutput` (~L320-333), `parseWholeBufferOpenclawResult` (~L483-503) |
| canonical `openclaw returned no parseable output` string is alert-coupled | `openclawNoParseableOutput` (~L22) |

## Family 4 — stateful JSON-RPC / ACP (codex / hermes / kimi / kiro)

| Claim | Source |
|---|---|
| `codex app-server --listen stdio://`; `--listen` blocked | codex.go `buildCodexArgs` (~L84-102); `codexBlockedArgs` (~L27-29) |
| JSON-RPC request plumbing (id, pending map, write, block on channel) | codex.go `codexClient.request` (~L1203-1248) |
| Lifecycle: initialize → initialized → thread/start|resume → turn/start | codex.go `Execute` (~L662-719); `startOrResumeThread` (~L895-948) |
| Server-request auto-approve `{"decision":"accept"}` / `{"action":"accept"}` / -32601 default | codex.go `handleServerRequest` (~L1352-1371) |
| Semantic-inactivity watchdog (10 min) + marker | `defaultCodexSemanticInactivityTimeout` (~L38), `CodexSemanticInactivityMarker` (~L52), select loop (~L786-804) |
| First-turn no-progress watchdog (30s, scaled) + marker | `defaultCodexFirstTurnNoProgressTimeout` (~L39), `CodexFirstTurnNoProgressMarker` (~L56), `codexFirstTurnNoProgressTimeout` (~L1024-1033), select (~L768-785) |
| Graceful shutdown (10s) for OTEL flush | `codexGracefulShutdownTimeout` (~L47), shutdown select (~L826-838) |
| Usage from RPC then JSONL fallback | codex.go (~L850-873), `scanCodexSessionUsage` (~L1725-1760) |
| Codex MCP via `$CODEX_HOME/config.toml` managed block at 0o600 | `ensureCodexMcpConfig` (~L213-285), `renderCodexMcpServersBlock` (~L297-356) |
| `hermes acp` + `HERMES_YOLO_MODE=1`; `acp` blocked | hermes.go `Execute` (~L61, L78-80); `hermesBlockedArgs` (~L28-30) |
| ACP lifecycle: initialize → session/new|resume → session/set_model → session/prompt | hermes.go `Execute` (~L207-340) |
| session/set_model MUST fail task on error | hermes.go `Execute` (~L294-322) |
| Notification normalization + update types | `normalizeACPUpdate` / `normalizeACPUpdateType` (~L804-847); `handleNotification` switch (~L788-801) |
| `streamingCurrentTurn` gate drops resume history replay | hermes.go (~L138-141, L334) |
| agent→client `session/request_permission` → `approve_for_session` | `handleAgentRequest` (~L590-637) |
| `isACPSessionNotFound` across hermes/-32603, kiro/data, kimi/-32602 | `isACPSessionNotFound` (~L666-677) |
| ACP MCP as `session/new` `mcpServers` array param; capability filter | `buildACPMcpServers` (~L1323-1356), `buildHermesSessionParams` (~L1286-1298), `extractACPMcpCapabilities` (~L1455-1471), `filterACPMcpServersByCapability` (~L1483-1521) |
| `end_turn` despite provider error → promote to failed (#1952) | `acpProviderErrorSniffer` (~L1614-1656), `promoteACPResultOnProviderError` (~L1765-1785) |

## Autonomy-flag table

| Row | Source |
|---|---|
| claude `bypassPermissions` + `control_response allow` | `buildClaudeArgs` (~L507), `handleControlRequest` (~L336-346) |
| gemini `--yolo` | `buildGeminiArgs` (~L253) |
| opencode `--dangerously-skip-permissions` | opencode.go `Execute` (~L52) |
| codex `{"decision":"accept"}` / `{"action":"accept"}` | `handleServerRequest` (~L1361-1366) |
| hermes `HERMES_YOLO_MODE=1` + `approve_for_session` | hermes.go `Execute` (~L79), `handleAgentRequest` (~L601-611) |

## Shared mechanism (cross-linked, not owned here)

| Claim | Source |
|---|---|
| `filterCustomArgs` + per-backend `*BlockedArgs` strips protocol-critical user args | claude.go `filterCustomArgs` (~L656-686), `blockedArgMode` (~L637-642) — documented in detail on add-a-backend |
