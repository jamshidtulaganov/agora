-- A task retry is represented by a new agent_task_queue row, so task_id already
-- scopes message sequence numbers to one execution attempt. Remove any legacy
-- duplicates before making that delivery identity enforceable.
WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY task_id, seq
               ORDER BY created_at ASC, id ASC
           ) AS duplicate_number
    FROM task_message
)
DELETE FROM task_message
WHERE id IN (
    SELECT id
    FROM ranked
    WHERE duplicate_number > 1
);

DROP INDEX IF EXISTS idx_task_message_task_id_seq;

CREATE UNIQUE INDEX idx_task_message_task_id_seq
    ON task_message(task_id, seq);
