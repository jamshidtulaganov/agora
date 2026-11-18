-- name: GetTelegramAssistantSession :one
SELECT s.* FROM telegram_assistant_session t
JOIN assistant_session s ON s.id = t.session_id
WHERE t.user_id = $1 AND t.chat_id = $2 AND s.user_id = t.user_id;

-- name: SetTelegramAssistantSession :exec
INSERT INTO telegram_assistant_session (user_id, chat_id, session_id)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, chat_id) DO UPDATE SET session_id = EXCLUDED.session_id, created_at = now();

-- name: ClearTelegramAssistantSession :exec
DELETE FROM telegram_assistant_session WHERE user_id = $1 AND chat_id = $2;

-- name: CreateTelegramAssistantDelivery :exec
INSERT INTO telegram_assistant_delivery (run_id, chat_id, reply_to_message_id, lang)
VALUES ($1, $2, $3, $4)
ON CONFLICT (run_id) DO NOTHING;

-- name: ClaimTelegramAssistantDelivery :one
-- At most once: a replayed run_finished event finds delivered_at already set.
UPDATE telegram_assistant_delivery
SET delivered_at = now()
WHERE run_id = $1 AND delivered_at IS NULL
RETURNING *;

-- name: GetAssistantRunReply :one
-- The text the assistant ended a run with. Replies carry no run id, so a run's
-- replies are the ones between its own user turn and the next user turn.
SELECT m.content FROM assistant_run r
JOIN assistant_message u ON u.id = r.message_id
JOIN assistant_message m ON m.session_id = r.session_id
WHERE r.id = $1
  AND m.role = 'assistant' AND m.content <> ''
  AND m.sequence > u.sequence
  AND NOT EXISTS (
      SELECT 1 FROM assistant_message n
      WHERE n.session_id = r.session_id AND n.role = 'user'
        AND n.sequence > u.sequence AND n.sequence < m.sequence
  )
ORDER BY m.sequence DESC
LIMIT 1;

-- name: ListPendingAssistantOperationsByRun :many
SELECT * FROM assistant_pending_operation
WHERE run_id = $1 AND status = 'pending' AND expires_at > now()
ORDER BY created_at ASC;

-- name: TrackTelegramChat :one
-- A group belongs to one workspace. Re-running /track for the same workspace
-- refreshes the title; it never moves a group another workspace already owns.
INSERT INTO telegram_tracked_chat (chat_id, workspace_id, title, enabled_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (chat_id) DO UPDATE SET title = EXCLUDED.title
WHERE telegram_tracked_chat.workspace_id = EXCLUDED.workspace_id
RETURNING *;

-- name: GetTelegramTrackedChat :one
SELECT * FROM telegram_tracked_chat WHERE chat_id = $1;

-- name: UntrackTelegramChat :execrows
DELETE FROM telegram_tracked_chat WHERE chat_id = $1;

-- name: ListTelegramTrackedChatsForUser :many
-- Only groups of workspaces the person belongs to. Group membership itself is
-- checked against Telegram at read time.
SELECT c.* FROM telegram_tracked_chat c
JOIN member m ON m.workspace_id = c.workspace_id
WHERE m.user_id = $1
ORDER BY c.title ASC, c.chat_id ASC;

-- name: RecordTelegramChatMessage :exec
-- An edited message arrives with the same id; keep the latest text.
INSERT INTO telegram_chat_message (chat_id, message_id, sender_id, sender_name, text, sent_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (chat_id, message_id) DO UPDATE SET text = EXCLUDED.text;

-- name: ListTelegramChatMessagesSince :many
-- Newest first so the caller keeps the most recent N and reverses.
SELECT * FROM telegram_chat_message
WHERE chat_id = $1 AND sent_at >= $2
ORDER BY sent_at DESC, message_id DESC
LIMIT $3;

-- name: PruneTelegramChatMessages :exec
DELETE FROM telegram_chat_message WHERE chat_id = $1 AND sent_at < $2;

-- name: SetTelegramTrackedChatRole :execrows
UPDATE telegram_tracked_chat SET role = $2 WHERE chat_id = $1;
