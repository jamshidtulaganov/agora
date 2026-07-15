DROP INDEX IF EXISTS idx_orchestration_step_squad;
DROP INDEX IF EXISTS idx_orchestration_step_parent;

ALTER TABLE orchestration_step
    DROP COLUMN IF EXISTS controller_agent_id,
    DROP COLUMN IF EXISTS squad_id;
