-- When the bot first saw each person in a tracked group. Someone can read the
-- group's history only from that moment on: joining a group must not hand over
-- what was said before they were there.
CREATE TABLE telegram_chat_participant (
    chat_id BIGINT NOT NULL REFERENCES telegram_tracked_chat(chat_id) ON DELETE CASCADE,
    telegram_user_id BIGINT NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, telegram_user_id)
);
