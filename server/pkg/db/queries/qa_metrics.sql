-- QA speed / regression metrics — aggregates for the QA Metrics page.
-- Every query takes an optional project_id (sqlc.narg): NULL = workspace-wide
-- (the "all projects" cockpit view), a UUID = scope to that one project so the
-- Metrics tab follows the same project selector as Bugs/Sprint. Runs/durations
-- are scoped through the owning issue's project_id; script coverage through
-- test_case.project_id.

-- name: QAMetricsRunTotals :one
-- Regression run totals over the window: how much the suite runs and how green.
SELECT count(*)                                   AS total,
       count(*) FILTER (WHERE r.status = 'pass')    AS passed,
       count(*) FILTER (WHERE r.status = 'fail')    AS failed,
       count(*) FILTER (WHERE r.status IN ('skip','blocked')) AS skipped
FROM test_run r
LEFT JOIN issue i ON i.id = r.issue_id
LEFT JOIN test_case tc ON tc.id = r.test_case_id
WHERE r.workspace_id = sqlc.arg('workspace_id') AND r.created_at > now() - interval '30 days'
  -- Scope through the owning issue OR the case's own project (base-suite /
  -- branch-level runs have no issue): without the COALESCE, those runs were
  -- counted in "All projects" but vanished when a project was selected, so
  -- per-project totals didn't add up (audit P2 asymmetry).
  AND (sqlc.narg('project_id')::uuid IS NULL OR COALESCE(i.project_id, tc.project_id) = sqlc.narg('project_id'));

-- name: QAMetricsRunsByDay :many
-- Daily regression volume + failures for the trend strip (last 14 days).
SELECT date_trunc('day', r.created_at)::date        AS day,
       count(*)                                   AS total,
       count(*) FILTER (WHERE r.status = 'fail')    AS failed
FROM test_run r
LEFT JOIN issue i ON i.id = r.issue_id
LEFT JOIN test_case tc ON tc.id = r.test_case_id
WHERE r.workspace_id = sqlc.arg('workspace_id') AND r.created_at > now() - interval '14 days'
  AND (sqlc.narg('project_id')::uuid IS NULL OR COALESCE(i.project_id, tc.project_id) = sqlc.narg('project_id'))
GROUP BY 1 ORDER BY 1;

-- name: QAMetricsAgentDurations :many
-- Per-QA-agent task wall-clock (created -> completed) over the window: the
-- agent-driven QA cost the compiled-script model is shrinking. QA agents =
-- members (or leader) of the workspace's squad named 'QA'.
SELECT a.name                                                        AS agent,
       count(*)                                                      AS runs,
       round(avg(EXTRACT(EPOCH FROM (atq.completed_at - atq.created_at))))::int AS avg_sec,
       round(min(EXTRACT(EPOCH FROM (atq.completed_at - atq.created_at))))::int AS min_sec,
       round(max(EXTRACT(EPOCH FROM (atq.completed_at - atq.created_at))))::int AS max_sec
FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
LEFT JOIN issue i ON i.id = atq.issue_id
WHERE a.workspace_id = sqlc.arg('workspace_id')
  AND atq.status = 'completed' AND atq.completed_at IS NOT NULL
  AND atq.created_at > now() - interval '30 days'
  AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id'))
  AND atq.agent_id IN (
    SELECT sm.member_id FROM squad_member sm
    JOIN squad s ON s.id = sm.squad_id
    WHERE s.workspace_id = sqlc.arg('workspace_id') AND s.name = 'QA' AND sm.member_type = 'agent'
    UNION
    SELECT s.leader_id FROM squad s WHERE s.workspace_id = sqlc.arg('workspace_id') AND s.name = 'QA'
  )
GROUP BY a.name ORDER BY avg_sec DESC;

-- name: QAMetricsScriptCoverage :one
-- Compiled-script adoption: automated cases carrying a runnable script run
-- deterministically (~seconds) instead of being LLM-driven (~minutes).
SELECT count(*) FILTER (WHERE kind = 'automated')                    AS automated,
       count(*) FILTER (WHERE kind = 'automated' AND script <> '')   AS scripted
FROM test_case
WHERE workspace_id = sqlc.arg('workspace_id') AND archived_at IS NULL
  AND (sqlc.narg('project_id')::uuid IS NULL OR project_id = sqlc.narg('project_id'));

-- name: QAMetricsRecentRuns :many
-- Latest regression verdicts with their case + issue for the runs table.
SELECT r.id, r.status, r.created_at, r.run_source,
       c.title AS case_title,
       i.number AS issue_number
FROM test_run r
JOIN test_case c ON c.id = r.test_case_id
LEFT JOIN issue i ON i.id = r.issue_id
WHERE r.workspace_id = sqlc.arg('workspace_id')
  AND (sqlc.narg('project_id')::uuid IS NULL OR COALESCE(i.project_id, c.project_id) = sqlc.narg('project_id'))
ORDER BY r.created_at DESC
LIMIT 25;
