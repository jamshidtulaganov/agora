-- name: UpsertMcpCredential :one
-- Add or rotate the sealed auth for a (workspace, server_name). Replacing lets a
-- user rotate a token without first deleting the row.
INSERT INTO mcp_credential (
    workspace_id, server_name, secret_encrypted, secret_last4, created_by
) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (workspace_id, server_name) DO UPDATE SET
    secret_encrypted = EXCLUDED.secret_encrypted,
    secret_last4 = EXCLUDED.secret_last4,
    updated_at = now()
RETURNING *;

-- name: ListMcpCredentials :many
-- Metadata only — never returns secret_encrypted, so the listing endpoint can't
-- leak a token.
SELECT id, workspace_id, server_name, secret_last4, created_at, updated_at, created_by
FROM mcp_credential
WHERE workspace_id = $1
ORDER BY server_name;

-- name: GetMcpCredentialsForWorkspace :many
-- Full rows including the sealed secret — for server-side injection only (the
-- daemon dispatch path merges the decrypted headers into the mcp_config entry).
SELECT * FROM mcp_credential WHERE workspace_id = $1;

-- name: DeleteMcpCredential :execrows
DELETE FROM mcp_credential WHERE workspace_id = $1 AND server_name = $2;
