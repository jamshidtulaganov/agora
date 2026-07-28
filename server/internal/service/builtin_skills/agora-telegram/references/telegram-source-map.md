# Source map — agora-telegram

Every claim in `SKILL.md` traced to code. If behaviour differs from the skill,
re-check here first.

## Commands

| Claim | Source |
|---|---|
| `agora telegram chats` / `send` / `ask` exist and nothing else | `server/cmd/agora/cmd_telegram.go` (`telegramCmd.AddCommand` in `init`) |
| `send` accepts a positional arg, `--text`, or `--stdin` | `cmd_telegram.go`, `telegramSendCmd.RunE` |
| `ask` requires at least two `--option` values | `cmd_telegram.go`, `telegramAskCmd.RunE` |
| `ask` prints the answer on stdout, progress on stderr | `cmd_telegram.go`, `telegramAskCmd.RunE` |
| `ask` exits non-zero when the question expires | `cmd_telegram.go`, `case "expired"` |
| Poll interval is 3s | `cmd_telegram.go`, `telegramAskPollInterval` |

## Endpoints

| Claim | Source |
|---|---|
| `GET /api/agents/me/telegram/chats` | `server/cmd/server/router.go`, `ListAgentTelegramChats` |
| `POST /api/agents/me/telegram/send` | `router.go`, `SendAgentTelegramMessage` |
| `POST /api/agents/me/telegram/ask` | `router.go`, `AskAgentTelegramQuestion` |
| `GET /api/agents/me/telegram/questions/{id}` | `router.go`, `GetAgentTelegramQuestion` |

## Authorization

| Claim | Source |
|---|---|
| Only a running agent may call these; humans are refused | `telegram_agent_api.go`, `resolveActingAgentInstallation` |
| The acting agent comes from the task token, not a parameter | `handler.go`, `resolveActor` (`X-Actor-Source: task_token`) |
| A chat not in `allowed_chat_ids` is refused, with no fallback | `telegram_agent_api.go`, `resolveAgentTargetChat` → `telegramChatAllowed` |
| An unnamed chat uses `telegram_installation.chat_id` | `telegram_agent_api.go`, `resolveAgentTargetChat` |
| The token never leaves the server | `telegram_installation.go`, `agentTelegramClient` (opens the sealed token in-process) |

## Questions

| Claim | Source |
|---|---|
| Questions are persisted, not held in memory | `server/migrations/180_telegram_question.up.sql` |
| The first tap wins | `telegram_installation.sql`, `AnswerTelegramQuestion` (`WHERE answer IS NULL`) |
| An expired question stops accepting answers | same query (`AND expires_at > now()`) |
| Only someone allowed to instruct the agent may answer | `telegram_agent_api.go`, `handleAgentCallback` → `telegramSenderAllowed` |
| The answer is read from stored options, never the callback payload | `telegram_agent_api.go`, `handleAgentCallback` (`question.Options[index]`) |
| Buttons are replaced with the outcome once answered | `telegram_agent_api.go`, `handleAgentCallback` → `EditButtons` |
| Default timeout 10 min, cap 60 min | `telegram_agent_api.go`, `telegramQuestionDefaultTimeout` / `telegramQuestionMaxTimeout` |
| At most 6 options | `telegram_agent_api.go`, `telegramQuestionMaxOptions` |

## No edit / no delete

There is no endpoint, no CLI command and no Bot API path exposed for editing or
deleting an agent's own message. The only `EditButtons` call is the platform
replacing a settled question's keyboard (`handleAgentCallback`).

## The autopilot report is not yours to send

| Claim | Source |
|---|---|
| The platform posts the run write-up on `autopilot:run_done` | `server/cmd/server/telegram_push_listeners.go`, `registerAutopilotReportListener` |
| A project report chat overrides the agent default | `server/internal/handler/telegram_report.go`, `autopilotReportChatID` + `chooseAutopilotDestination` |
| A squad report speaks through its executing leader when that bot can reach the destination | `server/internal/handler/telegram_report.go`, `autopilotSpeakerAgent` + `autopilotDestination` |
| The platform bot is the fallback for a configured project chat the agent bot cannot reach | `server/internal/handler/telegram_report.go`, `chooseAutopilotDestination` |
| A short, table-free report is sent as a message; anything else as a PDF | `server/internal/handler/telegram_report.go`, `SendAutopilotReport` → `replyNeedsDocument` |
| Agent messages are markdown, converted to Telegram HTML | `server/internal/integrations/telegram/markdown.go`, `MarkdownToHTML` / `SendMarkdown` |
