ALTER TABLE orchestration_step
    DROP COLUMN IF EXISTS missing_head_shas,
    DROP COLUMN IF EXISTS integrated_head_shas,
    DROP COLUMN IF EXISTS integration_status,
    DROP COLUMN IF EXISTS step_kind;
