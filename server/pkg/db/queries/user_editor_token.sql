-- Per-user editor tokens (see migration 147). Sealed PATs the daemon injects
-- into the user's co-code editor env; never returned raw to the frontend.

-- name: UpsertUserEditorToken :exec
INSERT INTO user_editor_token (user_id, provider, token_sealed)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, provider)
DO UPDATE SET token_sealed = EXCLUDED.token_sealed, updated_at = now();

-- name: ListUserEditorTokens :many
SELECT user_id, provider, token_sealed, created_at, updated_at
FROM user_editor_token
WHERE user_id = $1
ORDER BY provider;

-- name: DeleteUserEditorToken :execrows
-- :execrows so the handler can 404 on 0 rows (convention from #1661).
DELETE FROM user_editor_token WHERE user_id = $1 AND provider = $2;
