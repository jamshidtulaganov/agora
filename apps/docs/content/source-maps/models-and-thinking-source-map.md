# Source map — /developers/agentic/models-and-thinking

This sidecar traces every load-bearing claim on the `models-and-thinking` page to exact source symbols. Update it in the same PR as any cited symbol move (the page's Source files drift rule).

## server/pkg/agent/models.go

| Page claim | Symbol / line anchor |
| --- | --- |
| `Model` struct fields (ID/Label/Provider/Default/Free/Category/Thinking) and their doc semantics | `type Model struct` (~L29-51) |
| `Default` is a display hint, no execution effect; empty `agent.model` → `""` to backend | doc comment on `Model` (~L19-28) |
| `Free`/`Category` are branding only | field comments (~L34-43) |
| `ModelThinking{SupportedLevels, DefaultLevel}` runtime-native, rendered as-is | `type ModelThinking struct` (~L58-64) |
| `ThinkingLevel{Value, Label, Description}`; Value is literal CLI token | `type ThinkingLevel struct` (~L71-75) |
| `ListModels` switch on providerType; three strategies | `func ListModels` (~L104-167) |
| claude/codex static + thinking annotate; gemini pure static | switch cases `claude`/`codex`/`gemini` (~L106-115) |
| dynamic-discovery providers via `cachedDiscovery` | switch cases (~L116-163) |
| opencode key uses `discoveryCacheKey` | case `opencode` (~L143-146) |
| `ModelSelectionSupported` returns true unconditionally; retained hook | `func ModelSelectionSupported` (~L169-183) |
| `cachedDiscovery` 60s TTL, never caches empty (#3729) | `func cachedDiscovery`, `len(models)==0` skip (~L185-216) |
| `modelCacheTTL = 60 * time.Second` | const (~L89) |
| `discoveryCacheKey` appends `:executablePath` | `func discoveryCacheKey` (~L218-223) |
| `claudeStaticModels` default `claude-sonnet-4-6` | `func claudeStaticModels` (~L231-241) |
| gemini has no `models list`, exposes aliases | `func geminiStaticModels` comment (~L256-279) |
| opencode `models --verbose`, variants→levels, 15s timeout, plain retry | `func discoverOpenCodeModels` (~L425-459) |
| Agora free overlay: `agoraFreeModels`, `annotateAgoraFree`, branding only | `var agoraFreeModels` / `func annotateAgoraFree` (~L400-423) |
| pi `--list-models`, noise filter, normalize to provider/model | `discoverPiModels` / `isPiDiscoveryNoise` (~L637-746) |
| hermes/kimi/kiro/copilot → `discoverACPModels` via `acpDiscoveryProvider` | `discoverHermesModels`..`discoverCopilotModels` (~L758-828) |
| `acpDiscoveryProvider` config fields | `type acpDiscoveryProvider` (~L838-846) |
| ACP handshake: initialize id=1, session/new id=2, temp cwd, empty mcpServers | `func discoverACPModels` (~L854-974) |
| `parseACPSessionNewModels`: availableModels/currentModelId, Default match, nil-on-missing, camel+snake | `func parseACPSessionNewModels` (~L993-1043) |
| copilot `--acp`, fallback `copilotStaticModels`, `inferCopilotProvider` | `discoverCopilotModels`/`inferCopilotProvider` (~L332-345, L812-828) |
| antigravity `agy models`, TSV slug→Aliases, no static fallback | `discoverAntigravityModels` / `parseAntigravityModels` / `resolveAntigravityModel` |
| cursor `--list-models`, fallback `cursorStaticModels` (auto) | `discoverCursorModels`/`cursorStaticModels` (~L287-291, L1115-1138) |
| openclaw enumerates agents (model binding at agent level) | `discoverOpenclawAgents` (~L1207-1245) |
| codebuddy `--help` parse + `codebuddyStaticModels` fallback | `discoverCodebuddyModels`/`codebuddyStaticModels` (~L1390-1406, L1479-1487) |

## server/pkg/agent/thinking.go

| Page claim | Symbol / line anchor |
| --- | --- |
| thinkingCache keyed on (provider, executablePath, cliVersion) | `type thinkingCacheKey` (~L31-35) |
| `thinkingDiscoveryTTL = 10 * time.Minute` | const (~L42) |
| cliVersion invalidates on CLI bump | header comment (~L24-29) + `loadClaudeThinkingByModel`/`loadCodexThinkingByModel` `DetectVersion` (~L145, L289) |
| `resetThinkingCacheForTests` only non-TTL invalidation | `func resetThinkingCacheForTests` (~L67-71) |
| Claude `--effort` superset parse + per-model `claudeModelEffortAllow`, DefaultLevel "medium" | `annotateClaudeThinking`/`loadClaudeThinkingByModel`/`claudeModelEffortAllow` (~L104-166) |
| claude fallbacks: `claudeStaticEffortFallback` {low,medium,high}; drift→`claudeStaticEffortFullSuperset` | `claudeEffortSuperset` (~L119-186) |
| over-offer-and-let-CLI-reject; raw Title label for unknown | `projectClaudeLevels` (~L207-222) |
| codex `debug models --bundled` JSON, per-model levels+default | `codexDebugModelsArgs`/`parseCodexDebugModels` (~L313-359) |
| codex caches empty map on failure | `loadCodexThinkingByModel` (~L285-305) |
| codebuddy `--effort` (no max), shared levels, DefaultLevel medium | `annotateCodebuddyThinking`/`codebuddyEffortSuperset` (~L424-478) |
| opencode variants→levels (reasoning gate) | `annotateOpenCodeModelMetadata` (models.go ~L574-587) |
| Gate 1 `IsKnownThinkingValue`: empty ok, opencode variant-name, providerThinkingEnums, unknown→empty only | `func IsKnownThinkingValue` (~L624-636) |
| `providerThinkingEnums` only claude/codex/codebuddy | `var providerThinkingEnums` (~L591-613) |
| opencode variant-name validity (≤64, identifier-safe, no leading sep) | `isValidOpenCodeVariantName` (~L638-655) |
| Gate 2 `ValidateThinkingLevel`: empty ok, ListModels live, default-model resolution, opencode any-model fallback, fail-closed | `func ValidateThinkingLevel`/`anyModelSupportsThinkingValue` (~L515-571) |
| not flattened onto shared enum (MUL-2339) | header comment (~L13-22) |
| server 400 literal-invalid vs daemon soft-skip combination-invalid | `providerThinkingEnums` comment (~L573-590) |

## server/internal/daemon/daemon.go

| Page claim | Symbol / line anchor |
| --- | --- |
| model-list response wires Model+Thinking, `supported` from ModelSelectionSupported | `agent.ListModels`, `modelWire`, `"supported"` (~L1480-1542) |
| no-static-default rule: Agent.Model || entry.Model || "" passed through | model resolution block (~L3010-3025) |
| thinkingLevel from task.Agent.ThinkingLevel | (~L3026-3029) |
| ValidateThinkingLevel call; err→pass through, !ok→drop+log | guard block (~L3040-3057) |
| ExecOptions{Model, ThinkingLevel} assembly | `execOpts := agent.ExecOptions{...}` (~L3058-3070) |

Drift rule: a documented symbol/flag/field move updates the page **and** this sidecar in the same PR.
