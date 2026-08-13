-- Isolate Telegram conversations per human inside a shared group.
--
-- A group-wide session lets one member's context bleed into another's and
-- serializes the whole room behind the slowest request. The gateway contract
-- is one durable thread per (agent, chat, Telegram user), while the task
-- delivery row remembers the exact Telegram message each run must answer.
ALTER TABLE telegram_chat_session
    ADD COLUMN telegram_user_id bigint NOT NULL DEFAULT 0,
    ADD COLUMN last_engaged_at timestamptz NOT NULL DEFAULT now();

DROP INDEX IF EXISTS idx_telegram_chat_session_current;
CREATE INDEX idx_telegram_chat_session_current
    ON telegram_chat_session (agent_id, chat_id, telegram_user_id, created_at DESC);

CREATE TABLE telegram_task_delivery (
    task_id              uuid PRIMARY KEY REFERENCES agent_task_queue(id) ON DELETE CASCADE,
    agent_id             uuid NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    chat_id              text NOT NULL,
    telegram_user_id     bigint NOT NULL,
    reply_to_message_id  bigint NOT NULL,
    delivery_started_at  timestamptz,
    delivered_at         timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now()
);
