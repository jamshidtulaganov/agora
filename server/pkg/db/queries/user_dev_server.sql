-- name: GetUserDevServer :one
SELECT * FROM user_dev_server
WHERE project_id = $1 AND user_id = $2;

-- name: ListUserDevServersForProject :many
SELECT * FROM user_dev_server
WHERE project_id = $1
ORDER BY created_at ASC;

-- name: UpsertUserDevServer :one
INSERT INTO user_dev_server (workspace_id, project_id, user_id, base_url)
VALUES ($1, $2, $3, $4)
ON CONFLICT (project_id, user_id)
DO UPDATE SET base_url = EXCLUDED.base_url, updated_at = now()
RETURNING *;

-- name: DeleteUserDevServer :exec
DELETE FROM user_dev_server
WHERE project_id = $1 AND user_id = $2;
