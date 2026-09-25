-- name: GetZohoAccountByUser :one
SELECT * FROM zoho_account WHERE user_id = $1;

-- name: UpsertZohoAccount :one
-- Connect or reconnect: one account per person; a new grant replaces the old.
INSERT INTO zoho_account (
    user_id, connection_id, dc, refresh_token_encrypted, scopes, zoho_email, zoho_name,
    crm_user_id, crm_role, crm_profile, desk_org_id, desk_agent_id,
    desk_departments, status, checked_at
)
VALUES (
    @user_id, @connection_id, @dc, @refresh_token_encrypted, @scopes, @zoho_email, @zoho_name,
    @crm_user_id, @crm_role, @crm_profile, @desk_org_id, @desk_agent_id,
    @desk_departments, 'connected', now()
)
ON CONFLICT (user_id) DO UPDATE SET
    connection_id = EXCLUDED.connection_id,
    dc = EXCLUDED.dc,
    refresh_token_encrypted = EXCLUDED.refresh_token_encrypted,
    scopes = EXCLUDED.scopes,
    zoho_email = EXCLUDED.zoho_email,
    zoho_name = EXCLUDED.zoho_name,
    crm_user_id = EXCLUDED.crm_user_id,
    crm_role = EXCLUDED.crm_role,
    crm_profile = EXCLUDED.crm_profile,
    desk_org_id = EXCLUDED.desk_org_id,
    desk_agent_id = EXCLUDED.desk_agent_id,
    desk_departments = EXCLUDED.desk_departments,
    status = 'connected',
    checked_at = now(),
    updated_at = now()
RETURNING *;

-- name: DeleteZohoAccountByUser :one
DELETE FROM zoho_account WHERE user_id = $1 RETURNING *;

-- name: MarkZohoAccountReconnect :exec
-- Zoho rejected the grant (revoked, password reset, person left): keep the
-- row so the UI can say "reconnect", but stop using it.
UPDATE zoho_account SET status = 'reconnect', updated_at = now()
WHERE user_id = $1 AND status <> 'reconnect';

-- name: CreateZohoOAuthState :exec
-- connection_id is the workspace connector whose client the consent screen
-- was opened with; the callback must exchange the code with the same client.
INSERT INTO zoho_oauth_state (state, user_id, connection_id) VALUES ($1, $2, $3);

-- name: ConsumeZohoOAuthState :one
-- Single use: the row is deleted as it is read, and only honoured within 15
-- minutes of the Connect click.
DELETE FROM zoho_oauth_state
WHERE state = $1 AND created_at > now() - interval '15 minutes'
RETURNING user_id, connection_id;

-- name: PruneZohoOAuthStates :exec
DELETE FROM zoho_oauth_state WHERE created_at < now() - interval '1 hour';

-- name: InsertZohoCallLog :exec
INSERT INTO zoho_call_log (
    user_id, workspace_id, source, task_id, tool, object, record_count, duration_ms, error
) VALUES (
    @user_id, sqlc.narg('workspace_id'), @source, sqlc.narg('task_id'), @tool, @object, @record_count, @duration_ms, @error
);
