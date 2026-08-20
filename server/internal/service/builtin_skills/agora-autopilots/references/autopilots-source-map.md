# Autopilots source map

- `server/cmd/agora/cmd_autopilot.go` registers `list`, `get`, `create`, `update`, `delete`, `trigger`, `runs`, `trigger-add`, `trigger-update`, `trigger-delete`, and `trigger-rotate-url`.
- The CLI maps reads/writes to `/api/autopilots`, `/api/autopilots/{id}`, `/api/autopilots/{id}/trigger`, `/api/autopilots/{id}/runs`, and trigger subroutes.
- `server/internal/service/autopilot.go` has `DispatchAutopilot`, creates `autopilot_run`, and switches on `execution_mode`.
- `create_issue` calls `dispatchCreateIssue`; `run_only` calls `dispatchRunOnly`.
- `resolveAutopilotLeader` resolves squad-assigned autopilots to the squad leader.
- `AgentReadiness` blocks archived/runtime-unready agents before enqueue.
- `server/cmd/server/router.go` exposes authenticated `/api/autopilots` routes and unauthenticated webhook ingress `/api/webhooks/autopilots/{token}`.
- `server/internal/handler/telegram_issue_destination.go` resolves automation Telegram group notices: issue speaker bot, authorized workspace bot, then platform bot.
- `server/internal/handler/automation_api.go` exposes human-only failed-run retries and the message-variable/condition-field catalog; `automation_engine.go` projects upstream creator/assignee emails into rule facts; `automation_actions.go` retries only failed steps and expands Agora/upstream assignees, actor, and upstream source URL variables from issue metadata.
- `server/internal/handler/bitrix_sync.go` stamps responsible and creator identity metadata used by Bitrix-scoped notification rules and preserves a resolved human responsible ahead of squad fallbacks; `server/internal/handler/bitrix_import.go` loads normalized `bitrix_identity_aliases`; `server/internal/integrations/bitrix/client.go` selects both `RESPONSIBLE_ID` and `CREATED_BY` on every task fetch path.
