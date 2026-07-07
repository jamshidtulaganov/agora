-- Per-user editor tokens (see migrations 147 + 148). Sealed PATs the daemon
-- injects into the user's co-code editor env; never returned raw to the
-- frontend. workspace_id NULL = global default; a workspace row overrides the
-- global one for editors opened on that workspace's issues.

-- name: UpsertUserEditorTokenGlobal :exec
INSERT INTO user_editor_token (user_id, provider, token_sealed, workspace_id)
VALUES ($1, $2, $3, NULL)
ON CONFLICT (user_id, provider) WHERE workspace_id IS NULL
DO UPDATE SET token_sealed = EXCLUDED.token_sealed, updated_at = now();

-- name: UpsertUserEditorTokenWorkspace :exec
INSERT INTO user_editor_token (user_id, provider, token_sealed, workspace_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, provider, workspace_id) WHERE workspace_id IS NOT NULL
DO UPDATE SET token_sealed = EXCLUDED.token_sealed, updated_at = now();

-- name: ListUserEditorTokens :many
SELECT user_id, provider, token_sealed, workspace_id, created_at, updated_at
FROM user_editor_token
WHERE user_id = $1
ORDER BY provider, workspace_id NULLS FIRST;

-- name: ListUserEditorTokensForWorkspace :many
-- Resolution input for one editor launch: the user's global rows plus the
-- rows scoped to this workspace. The caller prefers the workspace row per
-- provider.
SELECT user_id, provider, token_sealed, workspace_id, created_at, updated_at
FROM user_editor_token
WHERE user_id = $1 AND (workspace_id IS NULL OR workspace_id = $2)
ORDER BY provider;

-- name: DeleteUserEditorToken :execrows
-- :execrows so the handler can 404 on 0 rows (convention from #1661).
-- workspace_id: NULL deletes the global row, a uuid deletes that override.
DELETE FROM user_editor_token
WHERE user_id = $1 AND provider = $2
  AND workspace_id IS NOT DISTINCT FROM sqlc.narg('workspace_id')::uuid;
