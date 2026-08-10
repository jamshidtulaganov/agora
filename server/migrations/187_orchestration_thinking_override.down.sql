ALTER TABLE agent_task_queue
    DROP COLUMN IF EXISTS thinking_level_override;

ALTER TABLE orchestration_step
    DROP COLUMN IF EXISTS thinking_level_override;
