# Source map: provider-matrix-internals.mdx

Traces every load-bearing claim in `provider-matrix-internals.mdx` to the exact
code it was read from. Update both files in the same PR when any symbol below
moves or is renamed.

## runtimeConfigPath — provider → filename table

- **File:** `server/internal/daemon/execenv/runtime_config.go`
- **Symbol:** `runtimeConfigPath(workDir, provider string) string` (~L178)
- Switch cases verbatim:
  - `claude`, `codebuddy` → `filepath.Join(workDir, "CLAUDE.md")`
  - `codex`, `copilot`, `opencode`, `openclaw`, `hermes`, `pi`, `cursor`, `kimi`, `kiro`, `antigravity` → `filepath.Join(workDir, "AGENTS.md")`
  - `gemini` → `filepath.Join(workDir, "GEMINI.md")`
  - `default` → `""`
- `""` => "skip config injection, prompt-only mode" is from `InjectRuntimeConfig` (~L163) which returns `content, nil` when `path == ""`; `CleanupRuntimeConfig` (~L315) returns `nil` when `path == ""`.
- Marker constants `runtimeMarkerBegin` / `runtimeMarkerEnd` and the `local_directory` non-clobber rationale: `writeRuntimeConfigFile` (~L213) + the doc comment on `CleanupRuntimeConfig`. Cross-linked to brief-injection rather than re-documented here.
- Brief content described as "CLI catalog + workflow steps + agent identity/persona + skills + project context" is `buildMetaSkillContent` (~L364).

## providerNeedsInlineSystemPrompt — inline injection set

- **File:** `server/internal/daemon/daemon.go`
- **Symbol:** `providerNeedsInlineSystemPrompt(provider string) bool` (~L2630)
- Returns `true` for `openclaw`, `kiro`, `kimi`; `false` default. Code block in the page is copied verbatim.
- `execOpts.SystemPrompt = runtimeBrief` assignment: `runTask`, inside `if providerNeedsInlineSystemPrompt(provider)` (~L3092-3094).
- Per-provider rationale (openclaw belt-and-suspenders + `prepareOpenclawConfig`; kiro/kimi opaque cwd; stuck-in-`todo` failure mode): comment block at ~L3071-3094.
- Hermes-excluded rationale (ACP, starts in task cwd, loads AGENTS.md itself, duplicate context bloats turns, triggers upstream safety filters): comment at ~L3088-3091.
- `inline_system_prompt` log field: `taskLog.Debug("invoking backend", ...)` (~L3103), value `execOpts.SystemPrompt != ""`.

## --append-system-prompt path

- **Reference backend:** `server/pkg/agent/claude.go`, `buildClaudeArgs` (~L500). The `opts.SystemPrompt != ""` → `--append-system-prompt` append is at ~L529-531. Code block copied verbatim.
- **Mirror backends (same pattern):**
  - `server/pkg/agent/codebuddy.go` ~L59-61
  - `server/pkg/agent/pi.go` ~L516-518 (flag also documented in the help comment ~L490)
- Note that `claude` is NOT in the inline set: confirmed by absence from `providerNeedsInlineSystemPrompt`. The flag is shared machinery; daemon only populates `execOpts.SystemPrompt` for the three inline providers.

## skillsDirPath — native skill-discovery table

- **File:** `server/internal/daemon/execenv/context.go`
- **Symbol:** `skillsDirPath(workDir, provider string) string` (~L172); wrapped by `resolveSkillsDir` (~L159) for MkdirAll/manifest bookkeeping.
- Switch cases verbatim:
  - `claude`, `codebuddy` → `.claude/skills`
  - `copilot` → `.github/skills`
  - `opencode` → `.opencode/skills`
  - `openclaw` → `skills` (workdir root; scanner reads `<workspaceDir>/skills/`, paired with `agents.defaults.workspace` pin)
  - `pi` → `.pi/skills`
  - `cursor` → `.cursor/skills`
  - `kimi` → `.kimi/skills`
  - `kiro` → `.kiro/skills`
  - `antigravity` → `.agents/skills`
  - `default` (incl. `gemini`, `hermes`) → `.agent_context/skills`
- "discovered automatically" vs ".agent_context/skills" wording per provider: the `## Skills` section switch in `buildMetaSkillContent` (`runtime_config.go` ~L702-738) — `claude`/`codebuddy` and the native group get "discovered automatically"; `gemini`/`hermes` and default get the `.agent_context/skills/` referral.

## Cross-links asserted in the page

- Product capability matrix: `apps/docs/content/docs/providers.mdx` (slug `/providers`) — columns "Session resumption", "MCP", "Skill injection path", "Model selection". Verified the page exists and the skill-path column matches `skillsDirPath`.
- Sibling agentic pages linked (all exist under `apps/docs/content/docs/developers/agentic/`): `agent-runtime-contract`, `brief-injection`, `backend-interface`. `conventions` at `apps/docs/content/docs/developers/conventions.mdx`.

## Drift rule

A documented symbol/flag/field/provider-string move updates `provider-matrix-internals.mdx` AND this sidecar in the same PR.
