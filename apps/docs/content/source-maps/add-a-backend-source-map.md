# Source map — Add a Backend

Sidecar for `apps/docs/content/docs/developers/agentic/add-a-backend.mdx`. Every claim in the page traces to one of the rows below. When a symbol moves or changes behavior, update the page and this file in the same PR.

## server/pkg/agent/agent.go

| Page claim | Symbol / line anchor | Notes |
| --- | --- | --- |
| `Backend` is the single interface; `Execute(ctx, prompt, opts) (*Session, error)` | `type Backend interface` / `Execute` | Verbatim signature. |
| `Execute` returns fast with non-nil `*Session`; error ⇒ run never started, `*Session` nil | doc comment on `Execute` + backend bodies | Timing contract documented in backend-interface page. |
| `ExecOptions` fields enumerated | `type ExecOptions struct` | `Cwd, Model, SystemPrompt, ThreadName, MaxTurns, Timeout, SemanticInactivityTimeout, ResumeSessionID, ExtraArgs, CustomArgs, McpConfig, ThinkingLevel, OpenclawMode`. |
| `ExtraArgs` before `CustomArgs`; ExtraArgs "currently read by claude and codex backends only" | field comments on `ExtraArgs`/`CustomArgs` | Page states gemini reads CustomArgs only, claude reads both. |
| Unsupported fields silently ignored; `ThinkingLevel`/`OpenclawMode` added incrementally | field comments on `ThinkingLevel`, `OpenclawMode` | "other backends ignore the field rather than fail". |
| `runContext`: `>0` deadline, `<=0` no deadline (watchdog), caller owns cancel | `func runContext` | MUL-3064 cited in comment. |
| `Session` = `Messages <-chan Message`, `Result <-chan Result` (one value then close) | `type Session struct` | |
| `MessageType` constants list | `MessageText`…`MessageLog` consts | Verbatim. |
| `Result.Status` value set | `Result` struct comment | `"completed", "failed", "aborted", "timeout", "cancelled"`. |
| `TokenUsage`, `Result.Usage map[string]TokenUsage` keyed by model | `type TokenUsage`, `Result.Usage` | |
| `Config{ExecutablePath, Env map[string]string, Logger *slog.Logger}` | `type Config struct` | |
| `New` is a hand-edited switch; `case "xxx": return &xxxBackend{cfg: cfg}` | `func New` switch | No plugin registry — switch only. |
| `default` error string supported-types list must be edited too | `default:` return in `New` | Page reproduces the exact error format and instructs adding `, xxx`. |
| Package doc comment + `New` doc comment list supported types | top-of-file comment, `// Supported types:` | Both must stay truthful. |
| `DetectVersion` → `detectCLIVersion` | `func DetectVersion` | |
| `launchHeaders` map: command+subcommand, minimal, no internal flags/env | `var launchHeaders` + comment | |
| `LaunchHeader` returns entry or `""` for unknown | `func LaunchHeader` | |

## server/pkg/agent/gemini.go (minimal template)

| Page claim | Symbol | Notes |
| --- | --- | --- |
| Minimal template: LookPath, runContext, buildGeminiArgs, CommandContext, hideAgentWindow, WaitDelay=10s, Dir, Env, stdout pipe, reader goroutine, return Session | `func (b *geminiBackend) Execute` | One-shot NDJSON-on-stdout backend. |
| Post-Wait status mapping (DeadlineExceeded→timeout, Canceled→aborted, waitErr→failed) | reader goroutine tail | Reproduced verbatim shape in page. |
| `buildGeminiArgs` honors `opts.Model` via `-m` only when non-empty | `func buildGeminiArgs` | Backs the "honor Model only via flag" rule. |
| `geminiBlockedArgs` example (`-p`, `--yolo`, `-o`) | `var geminiBlockedArgs` | Page shows `-p`/`-o` as the minimal illustration. |
| gemini reads `opts.CustomArgs` only (not ExtraArgs) | `buildGeminiArgs` last `append` | Contrast with claude. |
| `buildGeminiEnv` wraps `buildEnv`, defaults `GEMINI_CLI_TRUST_WORKSPACE` | `func buildGeminiEnv` | Cited in Env row of checklist. |

## server/pkg/agent/claude.go (full reference)

| Page claim | Symbol | Notes |
| --- | --- | --- |
| Full reference: stdin protocol, MCP config temp file via `--mcp-config`, control-request auto-approve, stderr tail, resume resolution | `func (b *claudeBackend) Execute`, `handleControlRequest`, `writeMcpConfigToTemp`, `resolveSessionID` | |
| `claudeBlockedArgs` protocol-critical set | `var claudeBlockedArgs` | `-p`, `--output-format`, `--input-format`, `--permission-mode`, `--mcp-config`, `--effort`. |
| `buildClaudeArgs` runs ExtraArgs then CustomArgs through `filterCustomArgs` | `func buildClaudeArgs` | Order ExtraArgs→CustomArgs. |
| `--model` only when `opts.Model != ""`; `--effort` only when `opts.ThinkingLevel != ""` | `buildClaudeArgs` | Backs "honor Model via flag / no Go-side default". |
| `filterCustomArgs` drops blocked flag, skips value for `blockedWithValue`, unshell-quotes | `func filterCustomArgs`, `unshellQuoteArg`, `stripSurroundingQuotes` | "intentionally narrow" comment. |
| `blockedArgMode` = `blockedWithValue` / `blockedStandalone` | `type blockedArgMode`, consts | |
| `buildEnv`/`mergeEnv`/`isFilteredChildEnvKey` strip internal Claude Code markers | `func buildEnv`, `mergeEnv`, `isFilteredChildEnvKey` | Env checklist row. |
| `trySend` drops on full channel; output reconstructed in Result.Output | `func trySend` | |
| stderr tail wiring: `newStderrTail(newLogWriter(...), agentStderrTailBytes)`, `withAgentStderr(finalError, "claude", Tail())` | Execute body lines ~85, ~232 | |
| `detectCLIVersion`/`extractVersionLine` parse first semver-shaped line, tolerate shell noise | `func detectCLIVersion`, `extractVersionLine` | |
| close-stdout-on-cancel goroutine | inner goroutine on `<-runCtx.Done()` | Checklist row. |

## server/pkg/agent/models.go

| Page claim | Symbol | Notes |
| --- | --- | --- |
| `ListModels` is a second hand-edited switch; `default` ⇒ `unknown agent type` | `func ListModels` | |
| Static catalog case (gemini) vs `cachedDiscovery(...)` case | `case "gemini"`, `case "cursor"` etc. | |
| `Model` fields `{ID, Label, Provider, Default, Free, Category, Thinking}` | `type Model struct` | |
| `Default` is display-only, no execution effect; empty model ⇒ daemon passes `""` | `Model.Default` comment | Backs the Model-default rule. |
| `cachedDiscovery` 60s TTL (`modelCacheTTL`), keyed on providerType, never caches empty | `func cachedDiscovery`, `const modelCacheTTL` | #3729 empty-result note. |
| Discovery skeleton: default path, LookPath guard returns empty list, 15s timeout, hideAgentWindow, parse Output | `discoverCursorModels`, `discoverOpenCodeModels`, `discoverPiModels` | 15s matches network-backed paths. |
| ACP CLIs reuse `discoverACPModels` via `acpDiscoveryProvider` (hermes/kimi/kiro) | `func discoverACPModels`, `type acpDiscoveryProvider`, `discoverHermesModels`/`discoverKimiModels`/`discoverKiroModels` | initialize + session/new, availableModels/currentModelId. |
| `ModelSelectionSupported` returns `true` unconditionally | `func ModelSelectionSupported` | |
| `geminiStaticModels` as static template; `claudeStaticModels` short-list discipline | `func geminiStaticModels`, `claudeStaticModels` | |

## server/pkg/agent/version.go

| Page claim | Symbol | Notes |
| --- | --- | --- |
| `MinVersions` map gates registration; absent type ⇒ no-op | `var MinVersions`, `func CheckMinVersion` | claude 2.0.0, codex 0.100.0, copilot 1.0.0. |
| `parseSemver`/`versionRe` extract major.minor.patch | `func parseSemver`, `var versionRe` | |
| `extractVersionLine` tolerates shell noise (chcp on Windows) | `func extractVersionLine` | #2516. |

## server/pkg/agent/thinking.go

| Page claim | Symbol | Notes |
| --- | --- | --- |
| `Model.Thinking` = `*ModelThinking` w/ `SupportedLevels`/`DefaultLevel`; `ThinkingLevel{Value,Label,Description}` | (types in models.go) `ModelThinking`, `ThinkingLevel` | |
| Two discovery styles: parse `--help` (claude/codebuddy) vs structured subcommand (codex) | `annotateClaudeThinking`/`claudeEffortRe`, `annotateCodebuddyThinking`/`codebuddyEffortRe`, `annotateCodexThinking`/`parseCodexDebugModels`/`codexDebugModelsArgs` | `codex debug models --bundled`. |
| Cache keyed `(provider, executablePath, cliVersion)`, `thinkingDiscoveryTTL` 10min | `thinkingCacheKey`, `const thinkingDiscoveryTTL` | |
| `providerThinkingEnums` + synchronous `IsKnownThinkingValue` server gate | `var providerThinkingEnums`, `func IsKnownThinkingValue` | OpenCode absent (variant names). |
| Discovery failure ⇒ `Thinking == nil` ⇒ UI hides picker | annotate* funcs | |
| `ValidateThinkingLevel` per-model live-catalog validation | `func ValidateThinkingLevel` | Referenced as the daemon pre-exec guard counterpart. |

## server/pkg/agent/stderr_tail.go

| Page claim | Symbol | Notes |
| --- | --- | --- |
| `agentStderrTailBytes` = 2048 | `const agentStderrTailBytes` | |
| `newStderrTail(inner, max)`, `Tail()` trims whitespace | `func newStderrTail`, `(*stderrTail).Tail` | |
| `withAgentStderr(msg, label, tail)` appends tail when non-empty | `func withAgentStderr` | |

## server/pkg/agent/proc_windows.go, proc_other.go

| Page claim | Symbol | Notes |
| --- | --- | --- |
| `hideAgentWindow(cmd)` no-op off Windows, hides console on Windows; call on every exec.Cmd | `func hideAgentWindow` (both build-tagged files) | |
