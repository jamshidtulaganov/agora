-- Policy Agent — the fleet watchdog. These queries power a workspace-scoped view
-- of agent SPEED + health over agent_task_queue: per-agent run duration / queue
-- wait, stalled runs (stuck in 'running'), recent failures, and looping issues
-- (an issue churning many tasks). agent_task_queue has no workspace_id, so every
-- query joins agent for the tenant filter.

-- name: GetAgentSpeedMetrics :many
-- Per-agent speed over terminal tasks in the last 7 days: counts, avg/p95 run
-- duration (started→completed) and avg queue wait (created→dispatched), failures.
SELECT
    a.id AS agent_id,
    a.name AS agent_name,
    count(*) AS task_count,
    count(*) FILTER (WHERE t.status = 'failed') AS failed_count,
    COALESCE(AVG(EXTRACT(EPOCH FROM (t.completed_at - t.started_at)))
        FILTER (WHERE t.started_at IS NOT NULL AND t.completed_at IS NOT NULL), 0)::float8 AS avg_run_seconds,
    COALESCE(percentile_cont(0.95) WITHIN GROUP (
        ORDER BY EXTRACT(EPOCH FROM (t.completed_at - t.started_at)))
        FILTER (WHERE t.started_at IS NOT NULL AND t.completed_at IS NOT NULL), 0)::float8 AS p95_run_seconds,
    COALESCE(AVG(EXTRACT(EPOCH FROM (t.dispatched_at - t.created_at)))
        FILTER (WHERE t.dispatched_at IS NOT NULL), 0)::float8 AS avg_queue_seconds
FROM agent_task_queue t
JOIN agent a ON a.id = t.agent_id
WHERE a.workspace_id = $1
  AND t.status IN ('completed', 'failed')
  AND t.completed_at >= now() - interval '7 days'
GROUP BY a.id, a.name
ORDER BY avg_run_seconds DESC;

-- name: ListStalledTasks :many
-- Tasks stuck in 'running' well past a normal run (no heartbeat column exists, so
-- started_at age is the stall signal) — the agent stalled / hit a context error /
-- looped without finishing.
SELECT t.id, t.agent_id, a.name AS agent_name, t.issue_id, t.started_at, t.attempt
FROM agent_task_queue t
JOIN agent a ON a.id = t.agent_id
WHERE a.workspace_id = $1
  AND t.status = 'running'
  AND t.started_at < now() - (sqlc.arg('stall_minutes')::int * interval '1 minute')
ORDER BY t.started_at ASC;

-- name: ListRecentFailedTasks :many
-- Recent failures with their classifier + error text (agent_error / timeout /
-- runtime_offline / runtime_recovery / manual).
SELECT t.id, t.agent_id, a.name AS agent_name, t.issue_id,
       t.failure_reason, t.error, t.started_at, t.completed_at, t.attempt
FROM agent_task_queue t
JOIN agent a ON a.id = t.agent_id
WHERE a.workspace_id = $1
  AND t.status = 'failed'
  AND t.completed_at >= now() - interval '24 hours'
ORDER BY t.completed_at DESC
LIMIT 50;

-- name: ListLoopingIssues :many
-- Loop signal: an issue churning many agent tasks in a short window (e.g. the
-- in_review re-fire loop) — threshold+ tasks in the last hour.
SELECT t.issue_id, count(*) AS task_count, max(t.created_at)::timestamptz AS last_task_at
FROM agent_task_queue t
JOIN agent a ON a.id = t.agent_id
WHERE a.workspace_id = $1
  AND t.created_at >= now() - interval '1 hour'
GROUP BY t.issue_id
HAVING count(*) >= sqlc.arg('loop_threshold')::bigint
ORDER BY task_count DESC;
