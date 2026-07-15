ALTER TABLE orchestration_step
    DROP CONSTRAINT IF EXISTS orchestration_step_capability_check,
    DROP COLUMN IF EXISTS capability;
