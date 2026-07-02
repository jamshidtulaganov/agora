-- Sprint QA-readiness — "is this sprint mergeable?" surface for the QA cockpit.

-- name: ListActiveSprintsForWorkspace :many
-- Active sprints across all of the workspace's projects (the readiness picker).
SELECT s.id, s.name, s.branch, s.project_id, p.title AS project_title,
       (SELECT count(*) FROM issue_to_sprint x WHERE x.sprint_id = s.id) AS issue_count
FROM sprint s
JOIN project p ON p.id = s.project_id
WHERE p.workspace_id = $1 AND s.status = 'active'
ORDER BY p.title, s.name;

-- name: LatestSprintRegressionRun :one
-- The most recent whole-branch regression autopilot run for a sprint (daily
-- backstop or sprint-end gate) — the "is the branch green?" signal. Keyed on
-- the sprint id stashed in the dispatch payload (autopilot_run has no sprint fk).
SELECT status, source, triggered_at, completed_at, failure_reason
FROM autopilot_run
WHERE trigger_payload->>'sprint_id' = $1
ORDER BY triggered_at DESC
LIMIT 1;

-- name: SprintReadinessRows :many
-- Per-issue QA readiness for one sprint: the human qa:pass/qa:fail label plus
-- the automated test_run tallies (any fail = regression). Drives the row list
-- and the "X/Y green, mergeable?" rollup.
SELECT i.id, i.number, i.title, i.status,
       EXISTS (SELECT 1 FROM issue_to_label il JOIN issue_label l ON l.id = il.label_id
               WHERE il.issue_id = i.id AND l.name = 'qa:pass') AS qa_pass,
       EXISTS (SELECT 1 FROM issue_to_label il JOIN issue_label l ON l.id = il.label_id
               WHERE il.issue_id = i.id AND l.name = 'qa:fail') AS qa_fail,
       (SELECT count(*) FROM test_run r WHERE r.issue_id = i.id AND r.status = 'pass') AS runs_pass,
       (SELECT count(*) FROM test_run r WHERE r.issue_id = i.id AND r.status = 'fail') AS runs_fail,
       (SELECT count(*) FROM test_run r WHERE r.issue_id = i.id) AS runs_total
FROM issue i
JOIN issue_to_sprint its ON its.issue_id = i.id
WHERE its.sprint_id = $1 AND i.workspace_id = $2
ORDER BY i.number;
