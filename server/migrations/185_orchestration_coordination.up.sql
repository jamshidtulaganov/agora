DROP INDEX idx_orchestration_run_active_issue;

ALTER TABLE orchestration_run
    DROP CONSTRAINT orchestration_run_status_check,
    ADD CONSTRAINT orchestration_run_status_check CHECK (status IN (
        'draft', 'running', 'waiting_approval', 'waiting_input', 'blocked',
        'completed', 'failed', 'cancelled'
    ));

CREATE UNIQUE INDEX idx_orchestration_run_active_issue
    ON orchestration_run(issue_id)
    WHERE status IN ('draft', 'running', 'waiting_approval', 'waiting_input', 'blocked');

ALTER TABLE orchestration_step
    DROP CONSTRAINT orchestration_step_status_check,
    ADD CONSTRAINT orchestration_step_status_check CHECK (status IN (
        'pending', 'queued', 'running', 'waiting_approval', 'waiting_input',
        'blocked', 'completed', 'failed', 'cancelled', 'skipped'
    ));

CREATE TABLE orchestration_message (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES orchestration_run(id) ON DELETE CASCADE,
    step_id UUID NOT NULL REFERENCES orchestration_step(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN (
        'instruction', 'progress', 'question', 'answer', 'blocker',
        'handoff', 'ack', 'escalation'
    )),
    actor_type TEXT NOT NULL DEFAULT 'system'
        CHECK (actor_type IN ('system', 'member', 'agent')),
    actor_id UUID,
    target_type TEXT NOT NULL DEFAULT 'run'
        CHECK (target_type IN ('run', 'step', 'human', 'controller', 'agent')),
    target_id UUID,
    body JSONB NOT NULL DEFAULT '{}',
    plan_version INT NOT NULL DEFAULT 1 CHECK (plan_version >= 1),
    correlation_id UUID NOT NULL DEFAULT gen_random_uuid(),
    causation_id UUID REFERENCES orchestration_message(id) ON DELETE SET NULL,
    reply_to_id UUID REFERENCES orchestration_message(id) ON DELETE SET NULL,
    idempotency_key TEXT NOT NULL,
    expects_reply BOOLEAN NOT NULL DEFAULT FALSE,
    acknowledged_at TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(run_id, idempotency_key)
);

CREATE INDEX idx_orchestration_message_run_created
    ON orchestration_message(run_id, created_at, id);
CREATE INDEX idx_orchestration_message_step_created
    ON orchestration_message(step_id, created_at, id);
CREATE INDEX idx_orchestration_message_open_reply
    ON orchestration_message(step_id, created_at DESC)
    WHERE expects_reply AND resolved_at IS NULL;

COMMENT ON TABLE orchestration_message IS
    'Durable issue-scoped coordination log. Comments are a human-readable projection, not the delivery mechanism.';
