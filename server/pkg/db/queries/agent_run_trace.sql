-- name: UpsertAgentRunTrace :one
-- Anchor a terminal agent run. Upsert on task_id so a re-reported completion
-- (parallel-agent race, daemon retry) refreshes the row instead of erroring.
INSERT INTO agent_run_trace (
    task_id, workspace_id, agent_id, issue_id, task_status, issue_status_at_run
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (task_id) DO UPDATE SET
    task_status = EXCLUDED.task_status,
    issue_status_at_run = EXCLUDED.issue_status_at_run,
    updated_at = now()
RETURNING *;

-- name: UpdateAgentRunTraceOutcome :exec
UPDATE agent_run_trace
SET final_issue_status = $2,
    human_revised = $3,
    reopened = $4,
    reaction_score = $5,
    outcome_label = $6,
    updated_at = now()
WHERE task_id = $1;

-- name: GetAgentRunTraceByTask :one
SELECT * FROM agent_run_trace WHERE task_id = $1;

-- name: ListSettledPendingRunTraces :many
-- Pending traces whose run finished before the settle cutoff, oldest first.
-- The outcome backfill sweep claims these, derives a label from live signals,
-- and writes it back via UpdateAgentRunTraceOutcome.
SELECT * FROM agent_run_trace
WHERE outcome_label = 'pending' AND created_at < $1
ORDER BY created_at ASC
LIMIT $2;

-- name: ListAgentRunTraces :many
SELECT * FROM agent_run_trace
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListRunTracesForExport :many
-- Dataset export feed: workspace-scoped traces, optionally filtered to one
-- outcome label (NULL = all), newest first, paginated. Each row is assembled
-- into a training example by joining issue + agent + task_message downstream.
SELECT * FROM agent_run_trace
WHERE workspace_id = sqlc.arg('workspace_id')
  AND (sqlc.narg('outcome')::text IS NULL OR outcome_label = sqlc.narg('outcome'))
ORDER BY created_at DESC
LIMIT sqlc.arg('lim') OFFSET sqlc.arg('off');
