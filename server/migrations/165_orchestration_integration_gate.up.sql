ALTER TABLE orchestration_step
    ADD COLUMN step_kind TEXT NOT NULL DEFAULT 'task'
        CHECK (step_kind IN ('task', 'integration')),
    ADD COLUMN integration_status TEXT NOT NULL DEFAULT 'not_required'
        CHECK (integration_status IN ('not_required', 'pending', 'complete', 'missing_heads', 'conflicts')),
    ADD COLUMN integrated_head_shas JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN missing_head_shas JSONB NOT NULL DEFAULT '[]';
