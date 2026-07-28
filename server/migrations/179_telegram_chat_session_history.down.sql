DROP INDEX IF EXISTS idx_telegram_chat_session_current;
-- Collapsing back to one row per chat has to discard the history it was added
-- to keep; newest wins.
DELETE FROM telegram_chat_session t
USING telegram_chat_session newer
WHERE t.agent_id = newer.agent_id AND t.chat_id = newer.chat_id
  AND t.created_at < newer.created_at;
ALTER TABLE telegram_chat_session DROP CONSTRAINT telegram_chat_session_pkey;
ALTER TABLE telegram_chat_session ADD PRIMARY KEY (agent_id, chat_id);
CREATE UNIQUE INDEX idx_telegram_chat_session_session ON telegram_chat_session(chat_session_id);
