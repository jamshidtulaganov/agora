DROP TABLE orchestration_message;

UPDATE orchestration_step
SET status = 'failed',
    error = COALESCE(error, 'orchestration coordination migration rolled back'),
    updated_at = now()
WHERE status IN ('waiting_input', 'blocked');

ALTER TABLE orchestration_step
    DROP CONSTRAINT orchestration_step_status_check,
    ADD CONSTRAINT orchestration_step_status_check CHECK (status IN (
        'pending', 'queued', 'running', 'waiting_approval', 'completed',
        'failed', 'cancelled', 'skipped'
    ));

DROP INDEX idx_orchestration_run_active_issue;

UPDATE orchestration_run
SET status = 'failed', updated_at = now()
WHERE status IN ('waiting_input', 'blocked');

ALTER TABLE orchestration_run
    DROP CONSTRAINT orchestration_run_status_check,
    ADD CONSTRAINT orchestration_run_status_check CHECK (status IN (
        'draft', 'running', 'waiting_approval', 'completed', 'failed', 'cancelled'
    ));

CREATE UNIQUE INDEX idx_orchestration_run_active_issue
    ON orchestration_run(issue_id)
    WHERE status IN ('draft', 'running', 'waiting_approval');
