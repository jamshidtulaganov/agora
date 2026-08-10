ALTER TABLE orchestration_step
    ADD COLUMN thinking_level_override TEXT;

ALTER TABLE agent_task_queue
    ADD COLUMN thinking_level_override TEXT;

COMMENT ON COLUMN orchestration_step.thinking_level_override IS
    'Creation/reroute-time reasoning snapshot for this persisted step. Empty string explicitly pins provider-default/no thinking.';

COMMENT ON COLUMN agent_task_queue.thinking_level_override IS
    'Per-task reasoning snapshot copied from an orchestration step and preserved across retry/failover.';
