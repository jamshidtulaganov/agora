ALTER TABLE agent_task_queue
    ADD COLUMN run_mode TEXT NOT NULL DEFAULT 'auto'
        CHECK (run_mode IN ('auto', 'debug', 'plan', 'build'));

COMMENT ON COLUMN agent_task_queue.run_mode IS
    'Per-run human execution override. auto derives behavior from issue type; debug, plan, and build override it for this task only.';
