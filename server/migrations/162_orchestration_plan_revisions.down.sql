DROP TABLE IF EXISTS orchestration_plan_revision;
ALTER TABLE orchestration_step
    DROP COLUMN IF EXISTS supersedes_step_id,
    DROP COLUMN IF EXISTS retired_in_version,
    DROP COLUMN IF EXISTS introduced_in_version,
    DROP COLUMN IF EXISTS parent_step_id;
ALTER TABLE orchestration_run DROP COLUMN IF EXISTS plan_version;
