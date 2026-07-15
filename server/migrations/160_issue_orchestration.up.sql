CREATE TABLE orchestration_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'running', 'waiting_approval', 'completed', 'failed', 'cancelled')),
    mode TEXT NOT NULL DEFAULT 'auto' CHECK (mode IN ('auto', 'manual')),
    policy JSONB NOT NULL DEFAULT '{}',
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_orchestration_run_active_issue
    ON orchestration_run(issue_id)
    WHERE status IN ('draft', 'running', 'waiting_approval');
CREATE INDEX idx_orchestration_run_issue_created
    ON orchestration_run(issue_id, created_at DESC);

CREATE TABLE orchestration_step (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES orchestration_run(id) ON DELETE CASCADE,
    step_key TEXT NOT NULL,
    title TEXT NOT NULL,
    stage TEXT NOT NULL CHECK (stage IN ('plan', 'dev', 'qa', 'review', 'release')),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'queued', 'running', 'waiting_approval', 'completed', 'failed', 'cancelled', 'skipped')),
    position INT NOT NULL,
    agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    model_override TEXT,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    depends_on_step_id UUID REFERENCES orchestration_step(id) ON DELETE SET NULL,
    approval_required BOOLEAN NOT NULL DEFAULT FALSE,
    approved_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    approved_at TIMESTAMPTZ,
    attempt INT NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    max_attempts INT NOT NULL DEFAULT 2 CHECK (max_attempts >= 1),
    instructions TEXT NOT NULL DEFAULT '',
    output JSONB,
    error TEXT,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(run_id, step_key),
    UNIQUE(run_id, position)
);

CREATE INDEX idx_orchestration_step_run_position
    ON orchestration_step(run_id, position);
CREATE INDEX idx_orchestration_step_task
    ON orchestration_step(task_id) WHERE task_id IS NOT NULL;

ALTER TABLE agent_task_queue
    ADD COLUMN orchestration_step_id UUID REFERENCES orchestration_step(id) ON DELETE SET NULL;
CREATE INDEX idx_agent_task_queue_orchestration_step
    ON agent_task_queue(orchestration_step_id) WHERE orchestration_step_id IS NOT NULL;

CREATE TABLE orchestration_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES orchestration_run(id) ON DELETE CASCADE,
    step_id UUID REFERENCES orchestration_step(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    actor_type TEXT NOT NULL DEFAULT 'system'
        CHECK (actor_type IN ('system', 'member', 'agent')),
    actor_id UUID,
    details JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_orchestration_event_run_created
    ON orchestration_event(run_id, created_at, id);
