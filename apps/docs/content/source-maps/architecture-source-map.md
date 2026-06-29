# architecture — source map

This page (`developers/architecture.mdx`) is traced to the following repo symbols. If any symbol below is renamed, moved, or changes shape, update the page **and** this sidecar in the same PR (the built-in skills contract: source-traced docs must not drift).

| File | Symbols this page depends on |
|---|---|
| `server/internal/service/task.go` | `TaskService` struct; enqueue entry points `EnqueueTaskForIssue`, `EnqueueTaskForMention`, `EnqueueTaskForSquadLeader`, `EnqueueChatTask`, `EnqueueQuickCreateTask`; `QuickCreateContext` + `QuickCreateContextType`; sentinels `ErrChatTaskAgentArchived`, `ErrChatTaskAgentNoRuntime`; claim path `ClaimTask`, `ClaimTaskForRuntime`, `claimResponseRecoveryWindow` (90s), `ReclaimStaleDispatchedTaskForRuntime`, `EmptyClaimCache` fast path; transitions `StartTask`, `CompleteTask`; wakeup `NotifyTaskEnqueued`; broadcast ordering (queued before dispatch) |
| `server/pkg/agent/agent.go` | `Backend` interface + `Execute`; `ExecOptions` fields (`Cwd`, `Model`, `SystemPrompt`, `MaxTurns`, `Timeout`, `ResumeSessionID`, `ThinkingLevel`, `OpenclawMode`, `ExtraArgs`, `CustomArgs`); `Session` (`Messages`, `Result` channels); `Message` + `MessageType` constants (`MessageText`, `MessageThinking`, `MessageToolUse`, `MessageToolResult`, `MessageStatus`, `MessageError`, `MessageLog`); `Result` + `Result.Status` values (`completed`, `failed`, `aborted`, `timeout`, `cancelled`); `New` switch over 13 backends (`claude`, `codebuddy`, `codex`, `copilot`, `opencode`, `openclaw`, `hermes`, `gemini`, `pi`, `cursor`, `kimi`, `kiro`, `antigravity`); `LaunchHeader` |
| `server/internal/daemon/daemon.go` | `Daemon` struct; `Daemon.New` (sets `runner = d.runTask`); `Daemon.Run` (resolveAuth → preflightAuth → register → background loops → pollLoop); `handleTask` (local-dir lock, active-env-root guard, watchTaskCancellation, runner.run); `runTask`; `executeAndDrain` (drives `agent.Backend`, drains messages, idle watchdog, session pin, returns `agent.Result` + tool count); `reportTaskResult` (fail-closed: only `completed` → `CompleteTask`, else `FailTask`) |
| `server/cmd/server/router.go` | `chi.NewRouter()`; `r.Route("/api/daemon", …)`; daemon endpoints `/runtimes/{runtimeId}/tasks/claim` (`ClaimTaskByRuntime`), `/tasks/{taskId}/start` (`StartTask`), `/tasks/{taskId}/complete` (`CompleteTask`), `/tasks/{taskId}/fail` (`FailTask`) |
| `server/cmd/server/main.go` | `realtime.NewHub()` (workspace WS), `daemonws.NewHub()` (daemon WS) |
| `server/internal/handler/issue.go` | polymorphic assignees: `assignee_type` + `assignee_id`; `assignee_type` values `member` / `agent` / `squad`; agent-eligibility check (`assignee_type == "agent"`) |
| `server/pkg/db/queries/` → `server/pkg/db/generated/` | `sqlc` codegen (`make sqlc`); `Queries.*` methods (`CreateAgentTask`, `ClaimAgentTask`, `StartAgentTask`, `CompleteAgentTask`, `GetAgent`, `CountRunningTasks`) |

## Drift rule

When a documented symbol/flag/field above moves or changes, update `architecture.mdx` and this `architecture-source-map.md` in the same PR. The page hard-codes the `Result.Status` enum, the 13-backend `New` switch, the daemon endpoint paths, and the assignee enum (`member`/`agent`/`squad`) as contracts — a change to any of these without a matching doc update silently teaches stale behavior.
