-- name: UpsertZohoConnection :one
-- Add or replace the workspace's single Zoho connection. Rotating credentials
-- refreshes the probe columns so a stale 'invalid' verdict never outlives the
-- token that earned it.
INSERT INTO zoho_connection (
    workspace_id, dc, client_id, client_secret_encrypted, refresh_token_encrypted,
    scopes, crm_org_id, desk_org_id, projects_portal_id, sprints_team_id,
    probe_status, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (workspace_id) DO UPDATE SET
    dc = EXCLUDED.dc,
    client_id = EXCLUDED.client_id,
    client_secret_encrypted = EXCLUDED.client_secret_encrypted,
    refresh_token_encrypted = EXCLUDED.refresh_token_encrypted,
    scopes = EXCLUDED.scopes,
    crm_org_id = EXCLUDED.crm_org_id,
    desk_org_id = EXCLUDED.desk_org_id,
    projects_portal_id = EXCLUDED.projects_portal_id,
    sprints_team_id = EXCLUDED.sprints_team_id,
    probe_status = EXCLUDED.probe_status,
    probed_at = now(),
    updated_at = now()
RETURNING *;

-- name: GetZohoConnectionForWorkspace :one
-- Full row including sealed secrets — for server-side decryption only.
SELECT * FROM zoho_connection WHERE workspace_id = $1;

-- name: DeleteZohoConnection :execrows
DELETE FROM zoho_connection WHERE workspace_id = $1;

-- name: UpdateZohoConnectionProbe :exec
UPDATE zoho_connection
SET probe_status = $2, probed_at = now(), updated_at = now()
WHERE workspace_id = $1;
