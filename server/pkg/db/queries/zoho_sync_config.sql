-- name: CreateZohoSyncConfig :one
INSERT INTO zoho_sync_config (
    workspace_id, connection_id, channel, module_api_name, project_id,
    enabled, direction, field_map, status_map, filter_coql
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetZohoSyncConfig :one
SELECT * FROM zoho_sync_config WHERE id = $1;

-- name: GetZohoSyncConfigByModule :one
-- Outbound mirror lookup: an issue's zoho_rec_id names the module; the
-- (workspace, channel, module) unique key resolves its config in one probe.
SELECT * FROM zoho_sync_config
WHERE workspace_id = $1 AND channel = $2 AND module_api_name = $3;

-- name: ListZohoSyncConfigsForWorkspace :many
SELECT * FROM zoho_sync_config
WHERE workspace_id = $1
ORDER BY created_at ASC;

-- name: ListEnabledZohoSyncConfigs :many
-- Poller sweep input: every enabled config across all workspaces.
SELECT * FROM zoho_sync_config
WHERE enabled = true
ORDER BY created_at ASC;

-- name: UpdateZohoSyncConfig :one
-- Partial update: every updatable column is COALESCE'd so a narrow caller
-- (e.g. the engine persisting an auto-created project_id) can never NULL out
-- the columns it didn't pass — the UpdateProject partial-COALESCE footgun.
UPDATE zoho_sync_config SET
    project_id = COALESCE(sqlc.narg('project_id'), project_id),
    enabled = COALESCE(sqlc.narg('enabled'), enabled),
    direction = COALESCE(sqlc.narg('direction'), direction),
    field_map = COALESCE(sqlc.narg('field_map'), field_map),
    status_map = COALESCE(sqlc.narg('status_map'), status_map),
    filter_coql = COALESCE(sqlc.narg('filter_coql'), filter_coql),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateZohoSyncConfigCursor :exec
UPDATE zoho_sync_config SET cursor = $2, updated_at = now()
WHERE id = $1;

-- name: DeleteZohoSyncConfig :execrows
-- Workspace-scoped so a cross-workspace config id deletes zero rows (404).
DELETE FROM zoho_sync_config WHERE id = $1 AND workspace_id = $2;
