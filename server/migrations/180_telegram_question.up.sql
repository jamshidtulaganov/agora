-- A question an agent asked a Telegram group, and the answer it is waiting for.
--
-- Exists because the human gate currently lives in the web app: an agent that
-- needs a decision ("deploy to staging?") stops, and someone has to notice in
-- Agora. The people who can answer are usually in the group already, so the
-- question should be asked where they are.
--
-- Persisted rather than held in memory: the agent process may be restarted, the
-- server may be redeployed, and the answer arrives minutes later from a
-- different request entirely. An in-memory channel would drop the decision and
-- the agent would report a timeout that never happened.
CREATE TABLE telegram_question (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id     uuid NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    chat_id      text NOT NULL,
    prompt       text NOT NULL,
    -- The offered choices, in order. Stored so the callback can validate an
    -- answer against what was actually asked: a callback payload is
    -- client-supplied and must never be trusted as the answer text.
    options      text[] NOT NULL,
    -- Telegram's message id, so the keyboard can be replaced once answered and
    -- a second person cannot answer a question that is already settled.
    message_id   bigint,
    answer       text,
    -- Who decided. A gate whose answer cannot be attributed is not a gate.
    answered_by  bigint,
    answered_at  timestamptz,
    expires_at   timestamptz NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- The poller resolves an inbound callback to its question.
CREATE INDEX idx_telegram_question_agent_chat ON telegram_question(agent_id, chat_id);
