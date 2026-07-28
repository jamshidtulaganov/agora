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

-- name: SetTelegramInstallationAccess :one
UPDATE telegram_installation
SET access_policy = $2, allowed_telegram_user_ids = $3, updated_at = now()
WHERE agent_id = $1 AND workspace_id = $4
RETURNING *;

-- name: CreateTelegramBindingToken :one
INSERT INTO telegram_binding_token (token_hash, workspace_id, agent_id, created_by, expires_at)
VALUES ($1, $2, $3, sqlc.narg('created_by'), $4)
RETURNING *;

-- name: ConsumeTelegramBindingToken :one
-- Single-use and time-bound in ONE statement: a concurrent redemption of the
-- same token matches zero rows rather than binding twice.
UPDATE telegram_binding_token
SET consumed_at = now()
WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > now()
RETURNING *;

-- name: DeleteExpiredTelegramBindingTokens :exec
DELETE FROM telegram_binding_token WHERE expires_at < now() - interval '1 day';

-- name: SetTelegramInstallationChats :one
UPDATE telegram_installation
SET allowed_chat_ids = $2, updated_at = now()
WHERE agent_id = $1
RETURNING *;

-- name: UpsertTelegramChatSession :one
INSERT INTO telegram_chat_session (agent_id, chat_id, chat_session_id)
VALUES ($1, $2, $3)
ON CONFLICT (chat_session_id) DO NOTHING
RETURNING *;

-- name: GetTelegramChatSession :one
-- The chat's CURRENT conversation: the newest mapping whose session is still
-- active. Older rows are kept so a late reply from a superseded session can
-- still find its way back to the chat that asked.
SELECT tcs.* FROM telegram_chat_session tcs
JOIN chat_session cs ON cs.id = tcs.chat_session_id
WHERE tcs.agent_id = $1 AND tcs.chat_id = $2 AND cs.status = 'active'
ORDER BY tcs.created_at DESC
LIMIT 1;

-- name: GetTelegramChatSessionBySession :one
-- Outbound: which chat asked the question this session answers.
SELECT * FROM telegram_chat_session WHERE chat_session_id = $1;

-- name: ArchiveTelegramChatSession :exec
-- /reset: retire the chat's current conversation. Archives the session rather
-- than dropping the mapping, so a task still running against it can still
-- deliver its answer.
-- Archives EVERY active session for the chat, not just the newest. The lookup
-- above resolves the newest active one, so leaving an older active row behind
-- would make /reset resume a stale conversation instead of starting a fresh
-- one — the exact failure /reset exists to fix.
UPDATE chat_session SET status = 'archived', updated_at = now()
WHERE status = 'active' AND id IN (
    SELECT tcs.chat_session_id FROM telegram_chat_session tcs
    WHERE tcs.agent_id = $1 AND tcs.chat_id = $2
);
