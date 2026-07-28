-- Keep every (chat → session) mapping, not just the current one.
--
-- The old primary key (agent_id, chat_id) allowed exactly one row per chat, so
-- opening a new session REPLACED the link. Any task still running against the
-- previous session then lost its route home: it produced an answer that could
-- never be delivered, and the group saw silence. That is not hypothetical —
-- session 49cbf848 wrote a reply nobody received.
--
-- Keyed on the session instead, so an old session stays resolvable for as long
-- as it might still answer. "Current" becomes the newest row whose session is
-- still active.
ALTER TABLE telegram_chat_session DROP CONSTRAINT telegram_chat_session_pkey;
ALTER TABLE telegram_chat_session ADD PRIMARY KEY (chat_session_id);

-- The unique index on chat_session_id is now redundant with the primary key.
DROP INDEX IF EXISTS idx_telegram_chat_session_session;

-- Resolving the current session for a chat.
CREATE INDEX idx_telegram_chat_session_current
    ON telegram_chat_session (agent_id, chat_id, created_at DESC);
