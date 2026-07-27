-- name: UpsertTelegramInstallation :one
-- Re-installing an agent's bot REPLACES the row. Accumulating rows would leave
-- stale tokens that each keep receiving updates for the same agent.
INSERT INTO telegram_installation (
    workspace_id, agent_id, bot_token_encrypted, bot_username, bot_user_id,
    chat_id, installer_user_id
) VALUES ($1, $2, $3, $4, $5, sqlc.narg('chat_id'), sqlc.narg('installer_user_id'))
ON CONFLICT (agent_id) DO UPDATE SET
    bot_token_encrypted = EXCLUDED.bot_token_encrypted,
    bot_username        = EXCLUDED.bot_username,
    bot_user_id         = EXCLUDED.bot_user_id,
    chat_id             = COALESCE(EXCLUDED.chat_id, telegram_installation.chat_id),
    installer_user_id   = EXCLUDED.installer_user_id,
    status              = 'active',
    updated_at          = now()
RETURNING *;

-- name: GetTelegramInstallationByAgent :one
SELECT * FROM telegram_installation WHERE agent_id = $1;

-- name: GetTelegramInstallationByBotUser :one
-- Inbound routing: an update carries the receiving bot's id, which identifies
-- the agent that owns it.
SELECT * FROM telegram_installation WHERE bot_user_id = $1 AND status = 'active';

-- name: ListTelegramInstallations :many
SELECT * FROM telegram_installation
WHERE workspace_id = $1
ORDER BY installed_at DESC;

-- name: ListActiveTelegramInstallations :many
-- Every active bot across all workspaces — the poller opens one long-poll loop
-- per installation at startup.
SELECT * FROM telegram_installation WHERE status = 'active' ORDER BY installed_at;

-- name: SetTelegramInstallationChat :one
UPDATE telegram_installation
SET chat_id = $2, updated_at = now()
WHERE agent_id = $1
RETURNING *;

-- name: DeleteTelegramInstallation :exec
DELETE FROM telegram_installation WHERE agent_id = $1 AND workspace_id = $2;

-- name: SetTelegramInstallationSession :one
UPDATE telegram_installation
SET chat_session_id = $2, updated_at = now()
WHERE agent_id = $1
RETURNING *;

-- name: GetTelegramInstallationBySession :one
-- Outbound routing: an assistant reply lands on a session, and this resolves
-- the bot and chat it should be posted to.
SELECT * FROM telegram_installation
WHERE chat_session_id = $1 AND status = 'active';
