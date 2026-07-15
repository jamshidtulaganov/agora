DROP TABLE IF EXISTS orchestration_event;
ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS orchestration_step_id;
DROP TABLE IF EXISTS orchestration_step;
DROP TABLE IF EXISTS orchestration_run;
