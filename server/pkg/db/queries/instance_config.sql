-- name: ListInstanceConfig :many
SELECT key, value, updated_at, updated_by FROM instance_config ORDER BY key;

-- name: UpsertInstanceConfig :one
INSERT INTO instance_config (key, value, updated_by, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (key) DO UPDATE
    SET value = EXCLUDED.value,
        updated_by = EXCLUDED.updated_by,
        updated_at = now()
RETURNING key, value, updated_at, updated_by;

-- name: DeleteInstanceConfig :exec
-- Reset a key back to its env/default by removing the override row.
DELETE FROM instance_config WHERE key = $1;
