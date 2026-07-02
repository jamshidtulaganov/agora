-- name: UpsertZohoUserBinding :one
-- Add or replace one user's Zoho binding in a workspace. Re-binding (new
-- grant code) refreshes the probe columns.
INSERT INTO zoho_user_binding (
    workspace_id, user_id, connection_id, refresh_token_encrypted,
    scopes, zoho_user_email, probe_status
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (workspace_id, user_id) DO UPDATE SET
    connection_id = EXCLUDED.connection_id,
    refresh_token_encrypted = EXCLUDED.refresh_token_encrypted,
    scopes = EXCLUDED.scopes,
    zoho_user_email = EXCLUDED.zoho_user_email,
    probe_status = EXCLUDED.probe_status,
    probed_at = now(),
    updated_at = now()
RETURNING *;

-- name: GetZohoUserBinding :one
-- Full row including the sealed token — server-side decryption only.
SELECT * FROM zoho_user_binding
WHERE workspace_id = $1 AND user_id = $2;

-- name: DeleteZohoUserBinding :execrows
DELETE FROM zoho_user_binding
WHERE workspace_id = $1 AND user_id = $2;
