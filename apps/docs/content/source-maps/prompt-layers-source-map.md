## prompt-layers — source map

This sidecar traces every claim on the `prompt-layers` page back to the exact symbol it depends on. When any symbol below moves, the page and this file must be updated in the **same PR** (the built-in skills are source-traced contracts; a drifted doc teaches stale behavior).

| File | Symbols this page depends on | Used for (claim on page) |
|---|---|---|
| `server/internal/daemon/prompt.go` | `BuildPrompt(task Task, provider string) string` | Layer 1 entry point; the five-branch dispatcher |
| `server/internal/daemon/prompt.go` | branch order: `ChatSessionID` → `buildChatPrompt`; `TriggerCommentID` → `buildCommentPrompt`; `AutopilotRunID` → `buildAutopilotPrompt`; `QuickCreatePrompt` → `buildQuickCreatePrompt`; else inline assignment | the ordered list of five task-kind branches |
| `server/internal/daemon/prompt.go` | `buildChatPrompt`, `buildCommentPrompt`, `buildAutopilotPrompt`, `buildQuickCreatePrompt` | per-branch descriptions of what each emits |
| `server/internal/daemon/prompt.go` | doc comment on `BuildPrompt` ("Keep this minimal — detailed instructions live in CLAUDE.md / AGENTS.md") | the "thin volatile" framing of Layer 1 |
| `server/internal/daemon/prompt.go` | `buildCommentPrompt` calls `execenv.BuildCommentReplyInstructions(provider, task.IssueID, task.TriggerCommentID)` | anti-drift: Layer 1 calls the shared reply builder |
| `server/internal/daemon/prompt.go` | `task.Agent != nil && strings.Contains(task.Agent.Instructions, "## Squad Operating Protocol")` | Layer 1 squad-leader detection by substring match |
| `server/internal/daemon/prompt.go` | `buildQuickCreatePrompt` field rules + `Run exactly one agora issue create --output json` output contract | quick-create exception (detailed rules live in Layer 1) |
| `server/internal/daemon/prompt.go` | autopilot branch line `Do not run agora issue get; this run does not have an issue ID.` | autopilot branch emits IDs + no-issue guard |
| `server/internal/daemon/execenv/runtime_config.go` | `buildMetaSkillContent(provider string, ctx TaskContextForEnv) string` | Layer 2 generator |
| `server/internal/daemon/execenv/runtime_config.go` | `InjectRuntimeConfig(workDir, provider string, ctx TaskContextForEnv)` calls `buildMetaSkillContent` then writes via `runtimeConfigPath` | Layer 2 written to runtime config file |
| `server/internal/daemon/execenv/runtime_config.go` | `runtimeConfigPath` mapping (claude/codebuddy→CLAUDE.md; codex/copilot/opencode/openclaw/hermes/pi/cursor/kimi/kiro/antigravity→AGENTS.md; gemini→GEMINI.md; default→"") | provider → config-file table |
| `server/internal/daemon/execenv/runtime_config.go` | section headers emitted by `buildMetaSkillContent` (`# Agora Agent Runtime`, `## Agent Identity`, `## Requesting User`, `## Task Initiator`, `## Workspace Context`, `## Available Commands`, `## Comment Formatting`, `## Showing Code Changes`, `## Repositories`, `## Project Context`, `## Issue Metadata`, `### Workflow`, `## Mentions`, `## Attachments`, `## Output`) | "fat stable scaffold" section list |
| `server/internal/daemon/execenv/runtime_config.go` | `runtimeMarkerBegin`, `runtimeMarkerEnd`, `runtimeManagedSeparator`, `writeRuntimeConfigFile`, `CleanupRuntimeConfig` | non-clobbering inject/cleanup of user config |
| `server/internal/daemon/execenv/runtime_config.go` | `hasIssueContext := ctx.ChatSessionID == "" && ctx.QuickCreatePrompt == "" && ctx.AutopilotRunID == ""`; `isAssignmentTriggered := ... && ctx.TriggerCommentID == ""` | guards a new task kind must update |
| `server/internal/daemon/execenv/runtime_config.go` | comment-branch step 7: `BuildCommentReplyInstructions(provider, ctx.IssueID, ctx.TriggerCommentID)` | anti-drift: Layer 2 calls the same shared reply builder |
| `server/internal/daemon/execenv/runtime_config.go` | `ctx.IsSquadLeader` branches in `### Workflow` and `## Output` | Layer 2 squad-leader detection via struct boolean |
| `server/internal/daemon/execenv/runtime_config.go` | quick-create `### Workflow` branch ("There is NO existing Agora issue ... ignore the default assignment-task workflow") + its code comment about one-shot single-source-of-truth | quick-create exception rationale |
| `server/internal/daemon/execenv/reply_instructions.go` | `BuildNewCommentsHint`, `BuildResumedCommentsHint`, `BuildColdCommentsHint`, `BuildCommentReplyInstructions` | the four shared builders enforcing the anti-drift rule |
| `server/internal/daemon/execenv/reply_instructions.go` | doc comments ("Both surfaces call this so the cold fallback cannot drift between them"; "hard requirement from PR #2816") | the explicit anti-drift contract |
| `server/internal/daemon/execenv/reply_instructions.go` | platform branch in `BuildCommentReplyInstructions` (Windows `--content-file`; Linux/macOS quoted HEREDOC) | platform-aware, provider-agnostic reply template |
| `server/internal/daemon/execenv/execenv.go` | `TaskContextForEnv` struct + discriminator fields `IssueID`, `ChatSessionID`, `TriggerCommentID`, `AutopilotRunID`, `QuickCreatePrompt`, `IsSquadLeader` | shared volatile struct; "new kind touches both layers" |
| `server/internal/daemon/execenv/execenv.go` | `IsSquadLeader` field comment ("true when the agent is acting as a squad leader (may exit silently on no_action)") | Layer 2 squad-leader boolean meaning |

### Drift rule

When any documented symbol, struct field, CLI flag, file path, or literal heading string (notably `## Squad Operating Protocol`, the runtime marker constants, and the `agora issue ...` command forms) moves or is renamed, update both the `prompt-layers` MDX page and this source map in the same PR. CI for the built-in skills treats this sidecar as the source-of-truth contract; a stale entry here is a build failure, not a cosmetic gap.