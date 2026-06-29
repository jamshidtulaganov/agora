-- name: UpsertGitCredential :one
-- Add or replace the credential for a (workspace, host, owner). Replacing lets a
-- user rotate a token without first deleting the row.
INSERT INTO git_credential (
    workspace_id, label, provider, host, owner, username, auth_kind, secret_encrypted, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (workspace_id, host, owner) DO UPDATE SET
    label = EXCLUDED.label,
    provider = EXCLUDED.provider,
    username = EXCLUDED.username,
    auth_kind = EXCLUDED.auth_kind,
    secret_encrypted = EXCLUDED.secret_encrypted
RETURNING *;

-- name: ListGitCredentials :many
-- Metadata only — never returns secret_encrypted, so the listing endpoint can't
-- leak a token.
SELECT id, workspace_id, label, provider, host, owner, username, auth_kind, created_at, created_by
FROM git_credential
WHERE workspace_id = $1
ORDER BY host, owner;

-- name: GetGitCredentialsForWorkspace :many
-- Full rows including the sealed secret — for server-side resolution only (the
-- daemon repo-auth path).
SELECT * FROM git_credential WHERE workspace_id = $1;

-- name: DeleteGitCredential :execrows
DELETE FROM git_credential WHERE id = $1 AND workspace_id = $2;
