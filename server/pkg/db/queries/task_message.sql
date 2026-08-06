-- name: CreateTaskMessage :one
INSERT INTO task_message (task_id, seq, type, tool, content, input, output)
VALUES ($1, $2, $3, $4, $5, $6, $7)
-- Delivery is at-least-once. Returning the persisted row on a replay lets the
-- handler republish after a DB-commit/process-crash window; websocket clients
-- already deduplicate by (task_id, seq).
ON CONFLICT (task_id, seq) DO UPDATE
SET seq = EXCLUDED.seq
RETURNING *;

-- name: ListTaskMessages :many
SELECT * FROM task_message
WHERE task_id = $1
ORDER BY seq ASC;

-- name: ListTaskMessagesSince :many
SELECT * FROM task_message
WHERE task_id = $1 AND seq > $2
ORDER BY seq ASC;

-- name: DeleteTaskMessages :exec
DELETE FROM task_message
WHERE task_id = $1;
