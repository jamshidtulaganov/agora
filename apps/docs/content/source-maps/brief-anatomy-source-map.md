brief-anatomy-source-map.md

# Brief Anatomy — source map

This page (`brief-anatomy.mdx`) is a source-traced walkthrough of the Agora brief builder. Every claim maps to one of three files under `server/internal/daemon/execenv/`. Keep this sidecar in lockstep with the page: a documented symbol/flag/field move updates both in the same PR.

## runtime_config.go

- `buildMetaSkillContent(provider string, ctx TaskContextForEnv) string` (func, ~line 364) — the section-by-section builder this whole page documents. The order of its `WriteString` calls IS the section order shown in the page's flowchart.
- Preamble: `# Agora Agent Runtime` + "You are a coding agent in the Agora platform…" (lines 367-368).
- `## Agent Identity` block — gate `ctx.AgentName != "" || ctx.AgentID != ""`, plus the `else if ctx.AgentInstructions != ""` fallback (lines 372-389).
- `## Requesting User` block — gate `strings.TrimSpace(ctx.RequestingUserProfileDescription) != ""`; name via `sanitizeNameForBriefMarkdown`; CRLF/CR→LF normalization; blockquote per line (lines 397-430).
- `## Task Initiator` block — gate `sanitizeNameForBriefMarkdown(ctx.InitiatorName) != ""`; branch on `ctx.InitiatorType == "agent"` vs member; email via `sanitizeEmailForBrief` (lines 443-454). MUL-2645.
- `## Workspace Context` block — gate `strings.TrimRight(ctx.WorkspaceContext, " \t\r\n") != ""`; embedded raw (trusted admin content) (lines 464-468).
- `## Available Commands` — static; `### Core` list + `### Squad maintenance` (lines 470-501). Inline `agora issue comment add` no-inline-`--content` warning (MUL-2904 / OKK-497).
- `## Comment Formatting` — static, branches on `runtimeGOOS == "windows"` (lines 512-521).
- `## Showing Code Changes` — static diff-in-comment contract (lines 528-533).
- `## Repositories` — gate `len(ctx.Repos) > 0` (lines 536-548).
- `## Project Context` — gate `ctx.ProjectID != "" || len(ctx.ProjectResources) > 0`; uses `formatProjectResource` (lines 553-568).
- `hasIssueContext := ctx.ChatSessionID == "" && ctx.QuickCreatePrompt == "" && ctx.AutopilotRunID == ""` (line 574).
- `## Issue Metadata` — gate `hasIssueContext` (lines 575-583).
- `isAssignmentTriggered := … && ctx.TriggerCommentID == ""` (line 585).
- `## Instruction Precedence` — gate `isAssignmentTriggered` (lines 586-591).
- `### Workflow` 5-branch if/else-if chain: `ChatSessionID` (595) → `QuickCreatePrompt` (605) → `AutopilotRunID` (619) → `TriggerCommentID` (646) → else assignment (672). `IsSquadLeader` sub-branches at 661/680. Comment branch step 3 selects from the reply_instructions.go hint builders (651-659); step 7 calls `BuildCommentReplyInstructions` (669).
- `## Sub-issue Creation` — inline gate `ctx.IssueID != "" && ctx.ChatSessionID == "" && ctx.QuickCreatePrompt == "" && ctx.AutopilotRunID == ""` (lines 697-700).
- `## Skills` — gate `len(ctx.AgentSkills) > 0`; lead-in switch on `provider` (claude/codebuddy; codex/copilot/opencode/openclaw/pi/cursor/kimi/kiro/antigravity; gemini/hermes; default) (lines 702-738).
- `## Mentions` — static (lines 740-754).
- `## Attachments` — static (lines 756-758) + `## Important: Always Use the agora CLI` static (lines 760-765).
- `## Output` — switch on `ctx.AutopilotRunID` / `ctx.QuickCreatePrompt` / default; default branches on `ctx.IsSquadLeader` (lines 767-786).
- `sanitizeNameForBriefMarkdown(name string) string` (lines 68-91) — CR/LF→space, drop C0/DEL, backslash-escape `* _ ` + backtick + ` \ [ ] <`, TrimSpace.
- `sanitizeEmailForBrief(email string) string` (lines 101-112) — trim, require `@`, reject control/DEL/space/`\`/backtick/`* < > [ ]`; no markdown escaping. MUL-2645.
- `formatProjectResource(r ProjectResourceForEnv) string` (lines 118-146) — `github_repo` special-case, JSON fallback.
- `runtimeGOOS` var (line 56) — `runtime.GOOS`, test-overridable.
- `InjectRuntimeConfig`, `runtimeConfigPath` (lines 163-189) — provider→config-file mapping (referenced for context; detailed in brief-injection).

## execenv.go

- `TaskContextForEnv` struct (lines 64-111) — the input that gates every conditional section. Field-by-field mapping in the page's gate table. Discriminator fields: `ChatSessionID`, `QuickCreatePrompt`, `AutopilotRunID`, `TriggerCommentID`, `IssueID`. Identity: `AgentName`, `AgentID`, `AgentInstructions`. User context: `RequestingUserName`, `RequestingUserProfileDescription`, `WorkspaceContext`. Initiator: `InitiatorType`, `InitiatorID`, `InitiatorName`, `InitiatorEmail`. Project: `ProjectID`, `ProjectTitle`, `ProjectResources`. Repos: `Repos`. Skills: `AgentSkills`. Squad: `IsSquadLeader`. Comment-read: `TriggerThreadID`, `NewCommentCount`, `NewCommentsSince`, `PriorSessionResumed`. Autopilot detail: `AutopilotID`, `AutopilotTitle`, `AutopilotDescription`, `AutopilotSource`, `AutopilotTriggerPayload`.
- `RepoContextForEnv` (lines 16-19), `ProjectResourceForEnv` (lines 26-31), `SkillContextForEnv` (lines 114-119).

## reply_instructions.go

- `BuildNewCommentsHint` (lines 25-52) — warm path, since-anchor, issue-wide count + thread cursor.
- `BuildResumedCommentsHint` (lines 62-76) — resumed-session no-delta path.
- `BuildColdCommentsHint` (lines 91-103) — cold path, thread-first read.
- `BuildCommentReplyInstructions(provider, issueID, triggerCommentID string) string` (lines 144-183) — OS-branched reply form (Windows `--content-file`, Linux/macOS quoted HEREDOC). MUL-2904 / MUL-1467.
- `activeThreadID` (lines 105-110) — `TriggerThreadID` else `TriggerCommentID`.

## Drift triggers

Update the page + this sidecar in the same PR when any of these change:
- A section heading string, its gate expression, or its position in `buildMetaSkillContent`.
- The `hasIssueContext` / `isAssignmentTriggered` definitions or the inline Sub-issue Creation gate.
- A new or renamed `TaskContextForEnv` field, especially a user-controlled one (must also be sanitized — see MUL-2645).
- The sanitizer character sets in `sanitizeNameForBriefMarkdown` / `sanitizeEmailForBrief`.
- The `provider` switch arms in the `## Skills` lead-in or the `## Output` task-kind switch.
- Any CLI flag quoted in the static sections (`--output json`, `--full-id`, `--content-file`, `--content-stdin`, `--thread`, `--recent`, `--before`, `--ref`, `--status`).