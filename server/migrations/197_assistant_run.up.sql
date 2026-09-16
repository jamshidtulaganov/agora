CREATE TABLE assistant_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES assistant_session(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    message_id UUID NOT NULL UNIQUE REFERENCES assistant_message(id) ON DELETE CASCADE,
    request_id UUID,
    request_content TEXT NOT NULL,
    request_context TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'failed', 'cancelled', 'interrupted')),
    context_workspace_id UUID REFERENCES workspace(id) ON DELETE SET NULL,
    context_timezone TEXT NOT NULL DEFAULT '',
    active_tool TEXT,
    error TEXT,
    lease_owner UUID,
    lease_expires_at TIMESTAMPTZ,
    cancel_requested BOOLEAN NOT NULL DEFAULT false,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX idx_assistant_run_request ON assistant_run(session_id, request_id) WHERE request_id IS NOT NULL;
CREATE UNIQUE INDEX idx_assistant_run_active ON assistant_run(session_id) WHERE status IN ('queued', 'running');
CREATE INDEX idx_assistant_run_session ON assistant_run(session_id, created_at DESC);
CREATE INDEX idx_assistant_run_lease ON assistant_run(lease_expires_at) WHERE status = 'running';

CREATE TABLE assistant_operation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES assistant_run(id) ON DELETE CASCADE,
    tool_call_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    arguments JSONB NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed', 'uncertain')),
    result JSONB,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(run_id, tool_call_id)
);

ALTER TABLE assistant_message ADD COLUMN run_id UUID REFERENCES assistant_run(id) ON DELETE SET NULL;
CREATE SEQUENCE assistant_message_sequence_seq;
ALTER TABLE assistant_message ADD COLUMN sequence BIGINT;
UPDATE assistant_message m SET sequence = ranked.sequence FROM (
    SELECT id, row_number() OVER (ORDER BY created_at, id) AS sequence
    FROM assistant_message
) ranked WHERE ranked.id = m.id;
SELECT setval('assistant_message_sequence_seq', COALESCE((SELECT max(sequence) FROM assistant_message), 1), (SELECT count(*) > 0 FROM assistant_message));
ALTER TABLE assistant_message ALTER COLUMN sequence SET NOT NULL;
ALTER TABLE assistant_message ALTER COLUMN sequence SET DEFAULT nextval('assistant_message_sequence_seq');
ALTER SEQUENCE assistant_message_sequence_seq OWNED BY assistant_message.sequence;
CREATE INDEX idx_assistant_message_sequence ON assistant_message(session_id, sequence);
