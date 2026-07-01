-- name: CreateSprint :one
INSERT INTO sprint (workspace_id, project_id, name, goal, status, start_date, end_date, branch)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING *;

-- name: GetSprint :one
SELECT * FROM sprint WHERE id = $1 AND workspace_id = $2;

-- name: ListSprintsByProject :many
SELECT * FROM sprint WHERE project_id = $1 ORDER BY COALESCE(start_date, created_at) DESC;

-- name: UpdateSprint :one
UPDATE sprint SET name = $3, goal = $4, status = $5, start_date = $6, end_date = $7, branch = $8, updated_at = now()
WHERE id = $1 AND workspace_id = $2 RETURNING *;

-- name: DeleteSprint :exec
DELETE FROM sprint WHERE id = $1 AND workspace_id = $2;

-- name: DeleteIssuesInSprint :execrows
-- Delete every issue attached to a sprint (child rows cascade via their FKs).
-- Used by DeleteSprint so a sprint's tasks go WITH it instead of being orphaned
-- to the backlog. Tenancy-guarded by workspace_id. Returns the count deleted.
DELETE FROM issue
USING issue_to_sprint its
WHERE its.issue_id = issue.id
  AND its.sprint_id = $1
  AND issue.workspace_id = $2;

-- name: MarkSprintCompleted :one
-- Flip a due sprint to 'completed' after its sprint-end QA has been dispatched,
-- so ListDueSprints stops matching it on the next scheduler tick (otherwise an
-- active+past-end_date sprint would re-dispatch every ~30s). Guarded by
-- workspace_id for tenancy and by status='active' so two concurrent ticks can't
-- both dispatch — only the first UPDATE matches a row.
UPDATE sprint SET status = 'completed', updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND status = 'active'
RETURNING *;

-- name: SetIssueSprint :exec
INSERT INTO issue_to_sprint (issue_id, sprint_id) VALUES ($1, $2)
ON CONFLICT (issue_id) DO UPDATE SET sprint_id = EXCLUDED.sprint_id, created_at = now();

-- name: RemoveIssueSprint :exec
DELETE FROM issue_to_sprint WHERE issue_id = $1;

-- name: GetSprintForIssue :one
SELECT s.* FROM sprint s JOIN issue_to_sprint i ON i.sprint_id = s.id WHERE i.issue_id = $1;

-- name: ListIssuesBySprint :many
SELECT i.* FROM issue i JOIN issue_to_sprint x ON x.issue_id = i.id WHERE x.sprint_id = $1 ORDER BY i.created_at;

-- name: ListDueSprints :many
-- Sprint-end QA dispatch: sprints whose window has closed but are still marked
-- active. The scheduler polls this (sprints start on arbitrary dates, so a fixed
-- cron can't express "2 weeks after THIS sprint's start"), dispatches the
-- sprint-end regression, then flips status to 'completed' so the row stops
-- matching here on the next tick. No project filter — the scheduler is global
-- and resolves each sprint's project from sprint.project_id.
SELECT * FROM sprint
WHERE status = 'active'
  AND end_date IS NOT NULL
  AND end_date <= now()
ORDER BY end_date ASC;

-- name: ListSprintIdsForIssues :many
-- Bulk variant: fetch each issue's sprint id in one round-trip so the issue
-- list/detail endpoints can fold sprint_id into each row without an N+1 from
-- the client. Mirrors ListLabelsForIssues. issue_to_sprint is keyed by
-- issue_id (PK), so this returns at most one row per issue.
SELECT issue_id, sprint_id
FROM issue_to_sprint
WHERE issue_id = ANY(sqlc.arg('issue_ids')::uuid[]);
