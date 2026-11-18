-- Group chats the platform bot is allowed to remember, so the Assistant can
-- summarize them.
--
-- Recording is OPT-IN per group: a row exists only after a workspace owner or
-- admin ran /track in that group, and /untrack deletes the row and, by cascade,
-- everything recorded. Without a row the bot stores nothing it sees.
CREATE TABLE telegram_tracked_chat (
    chat_id BIGINT PRIMARY KEY,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    title TEXT NOT NULL DEFAULT '',
    -- How the bot should behave in this group ("you are the release manager…"),
    -- set with /role. Empty means the plain Assistant.
    role TEXT NOT NULL DEFAULT '',
    enabled_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_telegram_tracked_chat_workspace ON telegram_tracked_chat(workspace_id);

-- Text only, and only for a limited time (see PruneTelegramChatMessages).
CREATE TABLE telegram_chat_message (
    chat_id BIGINT NOT NULL REFERENCES telegram_tracked_chat(chat_id) ON DELETE CASCADE,
    message_id BIGINT NOT NULL,
    sender_id BIGINT NOT NULL,
    sender_name TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL,
    sent_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (chat_id, message_id)
);
CREATE INDEX idx_telegram_chat_message_time ON telegram_chat_message(chat_id, sent_at);

-- When the bot first saw each person in a tracked group. Someone can read the
-- group's history only from that moment on: joining a group must not hand over
-- what was said before they were there.
CREATE TABLE telegram_chat_participant (
    chat_id BIGINT NOT NULL REFERENCES telegram_tracked_chat(chat_id) ON DELETE CASCADE,
    telegram_user_id BIGINT NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, telegram_user_id)
);
