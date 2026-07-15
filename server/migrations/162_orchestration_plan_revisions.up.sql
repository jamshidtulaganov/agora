ALTER TABLE orchestration_run
    ADD COLUMN plan_version INT NOT NULL DEFAULT 1 CHECK (plan_version >= 1);

ALTER TABLE orchestration_step
    ADD COLUMN parent_step_id UUID REFERENCES orchestration_step(id) ON DELETE CASCADE,
    ADD COLUMN introduced_in_version INT NOT NULL DEFAULT 1,
    ADD COLUMN retired_in_version INT,
    ADD COLUMN supersedes_step_id UUID REFERENCES orchestration_step(id) ON DELETE SET NULL;

CREATE TABLE orchestration_plan_revision (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES orchestration_run(id) ON DELETE CASCADE,
    version INT NOT NULL,
    actor_type TEXT NOT NULL CHECK (actor_type IN ('member', 'agent', 'system')),
    actor_id UUID,
    reason TEXT NOT NULL,
    patch JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, version)
);

CREATE INDEX idx_orchestration_revision_run_version
    ON orchestration_plan_revision(run_id, version);
