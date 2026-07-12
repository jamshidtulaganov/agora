-- Per-workspace release integrations (release-hub Thread B / Phase 2). The
-- sealed webhook URL + signing secret live in secret_encrypted; the LIST query
-- deliberately never selects it, exposing only `secret_encrypted IS NOT NULL`
-- as has_secret so the member-visible status endpoint cannot leak a URL.

-- name: ListReleaseIntegrationsByWorkspace :many
-- Metadata only — never returns secret_encrypted, so the listing endpoint can't
-- leak the sealed URL/signing secret.
SELECT id, workspace_id, kind, config, events, enabled, probe_status,
       (secret_encrypted IS NOT NULL)::boolean AS has_secret,
       created_by, created_at, updated_at
FROM release_integration
WHERE workspace_id = $1
ORDER BY created_at;

-- name: ListEnabledReleaseIntegrationsByWorkspace :many
-- Full rows INCLUDING the sealed secret — for the server-side dispatcher only
-- (registerReleaseOutbound decrypts the URL/signing secret to deliver).
SELECT * FROM release_integration
WHERE workspace_id = $1 AND enabled = true
ORDER BY created_at;

-- name: GetReleaseIntegration :one
-- Full row (with sealed secret) scoped to the workspace — the resolve step for
-- update/delete so a raw URL id can never address another workspace's row.
SELECT * FROM release_integration WHERE id = $1 AND workspace_id = $2;

-- name: InsertReleaseIntegration :one
INSERT INTO release_integration (
    workspace_id, kind, config, secret_encrypted, events, enabled, probe_status, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateReleaseIntegration :one
-- Full-row update scoped to (id, workspace). The handler passes the existing
-- sealed secret back through when the caller isn't rotating it, so a metadata
-- edit never drops the stored URL.
UPDATE release_integration SET
    kind = $3,
    config = $4,
    secret_encrypted = $5,
    events = $6,
    enabled = $7,
    probe_status = $8,
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: DeleteReleaseIntegration :execrows
DELETE FROM release_integration WHERE id = $1 AND workspace_id = $2;
