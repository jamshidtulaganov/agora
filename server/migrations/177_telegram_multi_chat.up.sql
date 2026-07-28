-- Multiple groups per agent, hamroh's access.json shape.
--
-- allowed_chat_ids replaces the single bound chat for AUTHORIZATION: a chat may
-- instruct the agent only if it is listed. chat_id stays, but now means only
-- "where unsolicited output goes" (autopilot reports) — a report needs one
-- destination, whereas questions can come from several rooms.
ALTER TABLE telegram_installation
    ADD COLUMN allowed_chat_ids bigint[] NOT NULL DEFAULT '{}',
    -- Who may run /allow and /deny from inside Telegram. Distinct from
    -- allowed_telegram_user_ids on purpose: being able to ASK an agent is not
    -- the same as being able to widen who else can.
    ADD COLUMN admin_telegram_user_ids bigint[] NOT NULL DEFAULT '{}';

-- Existing bound chat becomes the first allowed chat, so this migration does
-- not silently revoke a working installation.
UPDATE telegram_installation
SET allowed_chat_ids = ARRAY[chat_id::bigint]
WHERE chat_id IS NOT NULL AND chat_id ~ '^-?[0-9]+$';

-- One conversation per (agent, chat). A single shared session would merge two
-- groups' contexts into one thread — each room would see answers shaped by the
-- other's questions.
CREATE TABLE telegram_chat_session (
    agent_id        uuid NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    chat_id         text NOT NULL,
    chat_session_id uuid NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, chat_id)
);

-- Outbound resolves a finished session back to the chat that asked.
CREATE UNIQUE INDEX idx_telegram_chat_session_session ON telegram_chat_session(chat_session_id);

-- Carry the existing single-session link over so an in-flight conversation
-- keeps its thread.
INSERT INTO telegram_chat_session (agent_id, chat_id, chat_session_id)
SELECT agent_id, chat_id, chat_session_id
FROM telegram_installation
WHERE chat_session_id IS NOT NULL AND chat_id IS NOT NULL
ON CONFLICT DO NOTHING;
