-- The chat session backing this bot's group conversation.
--
-- Without it the session has to be found by matching a title prefix, which
-- breaks the moment a human renames the session in the web panel — and the
-- rename is invisible, so the agent would silently start a new conversation and
-- lose the thread. An explicit link cannot drift.
ALTER TABLE telegram_installation
    ADD COLUMN chat_session_id uuid REFERENCES chat_session(id) ON DELETE SET NULL;
