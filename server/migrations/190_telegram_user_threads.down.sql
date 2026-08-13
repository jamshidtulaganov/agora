DROP TABLE IF EXISTS telegram_task_delivery;

DROP INDEX IF EXISTS idx_telegram_chat_session_current;
ALTER TABLE telegram_chat_session
    DROP COLUMN IF EXISTS last_engaged_at,
    DROP COLUMN IF EXISTS telegram_user_id;
CREATE INDEX idx_telegram_chat_session_current
    ON telegram_chat_session (agent_id, chat_id, created_at DESC);
