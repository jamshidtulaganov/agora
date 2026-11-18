-- name: GetTelegramAssistantSession :one
SELECT s.* FROM telegram_assistant_session t
JOIN assistant_session s ON s.id = t.session_id
WHERE t.user_id = $1 AND s.user_id = t.user_id;

-- name: SetTelegramAssistantSession :exec
INSERT INTO telegram_assistant_session (user_id, session_id)
VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE SET session_id = EXCLUDED.session_id, created_at = now();

-- name: ClearTelegramAssistantSession :exec
DELETE FROM telegram_assistant_session WHERE user_id = $1;

-- name: CreateTelegramAssistantDelivery :exec
INSERT INTO telegram_assistant_delivery (run_id, chat_id, lang)
VALUES ($1, $2, $3)
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
