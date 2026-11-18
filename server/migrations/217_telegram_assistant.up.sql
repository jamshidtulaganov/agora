-- The platform bot as an Agora Assistant conversation.
--
-- One current conversation per person PER CHAT. A private chat and each group
-- keep separate conversations on purpose: an answer in a group is read by the
-- whole room, so it must never be drawn from what the person discussed with
-- the bot in private. The bot has no session switcher, so "/reset" replaces the
-- row rather than adding one. The session itself is an ordinary
-- assistant_session, so the same conversation shows up in the web app.
CREATE TABLE telegram_assistant_session (
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    -- The Telegram chat id: the person's own id for a private chat.
    chat_id TEXT NOT NULL,
    session_id UUID NOT NULL REFERENCES assistant_session(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, chat_id)
);

-- Which runs owe an answer to a Telegram chat. Keyed on the run, not the
-- session: a turn typed in the web app into the same conversation must not be
-- echoed to Telegram. delivered_at is claimed atomically, so a replayed
-- run_finished event cannot post the same answer twice.
CREATE TABLE telegram_assistant_delivery (
    run_id UUID PRIMARY KEY REFERENCES assistant_run(id) ON DELETE CASCADE,
    chat_id TEXT NOT NULL,
    -- In a group the answer replies to the message that asked; 0 in private.
    reply_to_message_id BIGINT NOT NULL DEFAULT 0,
    lang TEXT NOT NULL DEFAULT '',
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
