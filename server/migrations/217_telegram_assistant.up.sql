-- The platform bot's private chat is an Agora Assistant conversation.
--
-- One current conversation per person: the bot has no session switcher, so
-- "/reset" replaces the row rather than adding one. The session itself is an
-- ordinary assistant_session, so the same conversation shows up in the web app.
CREATE TABLE telegram_assistant_session (
    user_id UUID PRIMARY KEY REFERENCES "user"(id) ON DELETE CASCADE,
    session_id UUID NOT NULL REFERENCES assistant_session(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Which runs owe an answer to a Telegram chat. Keyed on the run, not the
-- session: a turn typed in the web app into the same conversation must not be
-- echoed to Telegram. delivered_at is claimed atomically, so a replayed
-- run_finished event cannot post the same answer twice.
CREATE TABLE telegram_assistant_delivery (
    run_id UUID PRIMARY KEY REFERENCES assistant_run(id) ON DELETE CASCADE,
    chat_id TEXT NOT NULL,
    lang TEXT NOT NULL DEFAULT '',
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
