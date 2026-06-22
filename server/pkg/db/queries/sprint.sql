-- name: CreateSprint :one
INSERT INTO sprint (workspace_id, project_id, name, goal, status, start_date, end_date)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: GetSprint :one
SELECT * FROM sprint WHERE id = $1 AND workspace_id = $2;

-- name: ListSprintsByProject :many
SELECT * FROM sprint WHERE project_id = $1 ORDER BY COALESCE(start_date, created_at) DESC;

-- name: UpdateSprint :one
UPDATE sprint SET name = $3, goal = $4, status = $5, start_date = $6, end_date = $7, updated_at = now()
WHERE id = $1 AND workspace_id = $2 RETURNING *;

-- name: DeleteSprint :exec
DELETE FROM sprint WHERE id = $1 AND workspace_id = $2;

-- name: SetIssueSprint :exec
INSERT INTO issue_to_sprint (issue_id, sprint_id) VALUES ($1, $2)
ON CONFLICT (issue_id) DO UPDATE SET sprint_id = EXCLUDED.sprint_id, created_at = now();

-- name: RemoveIssueSprint :exec
DELETE FROM issue_to_sprint WHERE issue_id = $1;

-- name: GetSprintForIssue :one
SELECT s.* FROM sprint s JOIN issue_to_sprint i ON i.sprint_id = s.id WHERE i.issue_id = $1;

-- name: ListIssuesBySprint :many
SELECT i.* FROM issue i JOIN issue_to_sprint x ON x.issue_id = i.id WHERE x.sprint_id = $1 ORDER BY i.created_at;
