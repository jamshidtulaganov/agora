-- Reverse of 209. Any task still parked in 'waiting_human' has to go
-- somewhere the restored CHECK constraint accepts; 'failed' is the honest
-- landing (the run ended and nobody answered) and keeps the row visible.
UPDATE agent_task_queue
SET status = 'failed',
    failure_reason = COALESCE(failure_reason, 'agent_blocked'),
    completed_at = COALESCE(completed_at, now())
WHERE status = 'waiting_human';

ALTER TABLE agent_task_queue DROP CONSTRAINT IF EXISTS agent_task_queue_status_check;
ALTER TABLE agent_task_queue ADD CONSTRAINT agent_task_queue_status_check
    CHECK (status IN ('queued', 'dispatched', 'running', 'waiting_local_directory',
                      'completed', 'failed', 'cancelled'));

DROP INDEX IF EXISTS idx_task_escalation_issue;
DROP INDEX IF EXISTS idx_task_escalation_workspace_open;
DROP INDEX IF EXISTS idx_task_escalation_open_issue;
DROP TABLE IF EXISTS task_escalation;
