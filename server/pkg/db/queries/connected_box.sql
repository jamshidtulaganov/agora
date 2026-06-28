-- name: CreateConnectedBox :one
INSERT INTO connected_box (
    workspace_id,
    owner_id,
    label,
    ssh_host,
    ssh_user,
    ssh_port,
    deploy_pubkey,
    repo_url,
    work_dir,
    project_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, sqlc.narg(project_id))
RETURNING *;

-- name: BindConnectedBoxProject :one
UPDATE connected_box
SET project_id = sqlc.narg(project_id), updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: ListConnectedBoxesByWorkspace :many
SELECT * FROM connected_box
WHERE workspace_id = $1
ORDER BY created_at DESC;

-- name: GetConnectedBox :one
SELECT * FROM connected_box
WHERE id = $1 AND workspace_id = $2;

-- name: UpdateConnectedBoxStatus :one
UPDATE connected_box
SET status = $3,
    last_error = $4,
    daemon_id = COALESCE(sqlc.narg(daemon_id), daemon_id),
    last_bootstrap_at = COALESCE(sqlc.narg(last_bootstrap_at), last_bootstrap_at),
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: UpdateConnectedBoxSync :one
UPDATE connected_box
SET status = $3,
    last_error = $4,
    last_branch = COALESCE(sqlc.narg(last_branch), last_branch),
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: DeleteConnectedBox :exec
DELETE FROM connected_box
WHERE id = $1 AND workspace_id = $2;
