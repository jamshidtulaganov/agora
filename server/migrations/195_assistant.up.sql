-- Agora Assistant — the product's own system-level AI, user-scoped and
-- cross-workspace. Deliberately NOT built on chat_session/agent_task_queue:
-- that pipeline is bound to one agent in one workspace and executes through a
-- daemon. These tables carry a conversation that belongs to a PERSON and can
-- act in every workspace they are a member of.
--
-- No workspace_id on either table — that is the point. Workspace scoping
-- happens per tool call, inside the executor, against the caller's
-- memberships.
CREATE TABLE assistant_session (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    title TEXT NOT NULL DEFAULT '',
    -- Default workspace for tool calls when the user doesn't name one.
    -- Nullable: a session may be purely cross-workspace. Updated to the
    -- workspace the user was in when they opened/last used the session.
    focus_workspace_id UUID REFERENCES workspace(id) ON DELETE SET NULL,
    -- Rolling conversation summary for history truncation.
    summary TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_assistant_session_user ON assistant_session(user_id, updated_at DESC);

CREATE TABLE assistant_message (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES assistant_session(id) ON DELETE CASCADE,
    role TEXT NOT NULL,              -- user | assistant | tool
    content TEXT NOT NULL DEFAULT '',
    tool_calls JSONB,                -- assistant role: requested calls
    tool_call_id TEXT,               -- tool role: which call this answers
    tool_name TEXT,                  -- tool role: renderable action chip
    tool_result JSONB,               -- tool role: structured result (links!)
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_assistant_message_session ON assistant_message(session_id, created_at);
