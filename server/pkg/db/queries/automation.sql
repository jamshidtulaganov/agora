-- name: CreateAutomation :one
INSERT INTO automation (
    workspace_id, project_id, name, description, enabled,
    trigger_type, trigger_config, conditions, actions, recipe_key,
    created_by_type, created_by_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: GetAutomation :one
SELECT * FROM automation WHERE id = $1 AND workspace_id = $2;

-- name: ListAutomationsForWorkspace :many
SELECT * FROM automation
WHERE workspace_id = $1
ORDER BY created_at DESC;

-- name: ListEnabledAutomationsForTrigger :many
-- The engine's hot path (idx_automation_ws_trigger): the workspace's enabled rules
-- for ONE trigger, oldest first so a deterministic order is applied when several
-- rules match the same event.
SELECT * FROM automation
WHERE workspace_id = $1 AND trigger_type = $2 AND enabled
ORDER BY created_at ASC;

-- name: UpdateAutomation :one
-- Full replace of the editable surface. COALESCE is deliberately NOT used: the
-- editor always sends the complete rule, and a partial write that silently kept an
-- old action list would run something the human no longer sees on screen.
UPDATE automation
SET name = $3,
    description = $4,
    enabled = $5,
    project_id = $6,
    trigger_type = $7,
    trigger_config = $8,
    conditions = $9,
    actions = $10,
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: SetAutomationEnabled :one
UPDATE automation
SET enabled = $3, updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: DeleteAutomation :execrows
DELETE FROM automation WHERE id = $1 AND workspace_id = $2;

-- name: RecordAutomationFired :exec
-- Counter bump, separate from the run row so a failed action still counts as a
-- firing (the run row carries the failure detail).
UPDATE automation
SET run_count = run_count + 1, last_run_at = now()
WHERE id = $1;

-- name: CountAutomationsForWorkspace :one
SELECT count(*) FROM automation WHERE workspace_id = $1;

-- name: CreateAutomationRun :one
INSERT INTO automation_run (
    automation_id, workspace_id, issue_id, trigger_type, status,
    actions_applied, detail, error
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListAutomationRuns :many
SELECT * FROM automation_run
WHERE automation_id = $1 AND workspace_id = $2
ORDER BY created_at DESC
LIMIT $3;

-- name: GetAutomationRun :one
SELECT * FROM automation_run
WHERE id = $1 AND automation_id = $2 AND workspace_id = $3;

-- name: ListAutomationRunsForWorkspace :many
SELECT * FROM automation_run
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: CountRecentAutomationRunsForIssue :one
-- Loop guard input: how many times this automation ATTEMPTED actions on this
-- issue inside the window. 'failed' counts too — a rule whose actions always
-- fail (Telegram unbound, agent deleted) would otherwise have no budget at all
-- and retry on every event. Only 'skipped' evaluations are excluded: they ran
-- nothing and must not consume the budget.
SELECT count(*) FROM automation_run
WHERE automation_id = $1
  AND issue_id = $2
  AND status IN ('applied', 'failed')
  AND created_at > now() - sqlc.arg(window_seconds)::int * interval '1 second';

-- name: LatestAppliedAutomationRunForIssue :one
-- Cooldown input: when this automation last attempted actions on this issue
-- ('failed' included, same reasoning as the count above).
SELECT * FROM automation_run
WHERE automation_id = $1 AND issue_id = $2 AND status IN ('applied', 'failed')
ORDER BY created_at DESC
LIMIT 1;
