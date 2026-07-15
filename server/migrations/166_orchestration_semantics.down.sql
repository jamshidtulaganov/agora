ALTER TABLE orchestration_run
    DROP COLUMN IF EXISTS controller_agent_id,
    DROP COLUMN IF EXISTS owner_id,
    DROP COLUMN IF EXISTS owner_type,
    DROP COLUMN IF EXISTS progression_policy,
    DROP COLUMN IF EXISTS execution_strategy;

