-- name: UpsertFigmaCredential :one
-- Add or replace the workspace's single Figma credential. Rotating the token
-- resets the expiry-warning dedup (expiry_notified_at) so a fresh token gets a
-- fresh warning cycle.
INSERT INTO figma_credential (
    workspace_id, label, token_encrypted, token_last4, token_kind, expires_at,
    seat_probe, probe_status, probed_at, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now(), $9)
ON CONFLICT (workspace_id) DO UPDATE SET
    label = EXCLUDED.label,
    token_encrypted = EXCLUDED.token_encrypted,
    token_last4 = EXCLUDED.token_last4,
    token_kind = EXCLUDED.token_kind,
    expires_at = EXCLUDED.expires_at,
    seat_probe = EXCLUDED.seat_probe,
    probe_status = EXCLUDED.probe_status,
    probed_at = now(),
    expiry_notified_at = NULL,
    updated_at = now()
RETURNING *;

-- name: GetFigmaCredentialForWorkspace :one
-- Full row including the sealed token — for server-side injection/probing only.
SELECT * FROM figma_credential WHERE workspace_id = $1;

-- name: DeleteFigmaCredential :execrows
DELETE FROM figma_credential WHERE workspace_id = $1;

-- name: UpdateFigmaCredentialProbe :exec
UPDATE figma_credential
SET probe_status = $2, probed_at = now(), updated_at = now()
WHERE workspace_id = $1;

-- name: SetFigmaCredentialExpiryNotified :exec
UPDATE figma_credential
SET expiry_notified_at = now(), updated_at = now()
WHERE workspace_id = $1;

-- name: ListFigmaCredentialsForProbe :many
-- All credentials, for the nightly probe loop (Phase 6). Metadata + sealed
-- token; the loop decrypts per row.
SELECT * FROM figma_credential ORDER BY created_at;
