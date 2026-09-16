-- name: CreateAssistantSession :one
INSERT INTO assistant_session (user_id, title, focus_workspace_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetAssistantSession :one
SELECT * FROM assistant_session
WHERE id = $1;

-- name: ListAssistantSessions :many
SELECT * FROM assistant_session
WHERE user_id = $1
ORDER BY updated_at DESC
LIMIT $2;

-- name: UpdateAssistantSession :one
-- COALESCE semantics: a PATCH that omits a field leaves it untouched.
--
-- focus_workspace_id is the three-way case, and it follows the same
-- sqlc.narg + empty-string-sentinel convention as "user".timezone (see
-- pkg/db/queries/user.sql): no value at all leaves the column alone, the empty
-- string CLEARS it to NULL, and a UUID pins it. Plain COALESCE cannot express
-- the middle case — it reads NULL as "leave it alone" — which is why removing
-- the assistant's context chip used to be a silent no-op that reappeared on the
-- next refetch.
UPDATE assistant_session
SET title              = COALESCE(sqlc.narg('title'), title),
    focus_workspace_id = CASE
        WHEN sqlc.narg('focus_workspace_id')::text IS NULL THEN focus_workspace_id
        WHEN sqlc.narg('focus_workspace_id')::text = ''    THEN NULL
        ELSE (sqlc.narg('focus_workspace_id')::text)::uuid
    END,
    summary            = COALESCE(sqlc.narg('summary'), summary),
    updated_at         = now()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: TouchAssistantSession :exec
UPDATE assistant_session
SET updated_at = now()
WHERE id = $1;

-- name: DeleteAssistantSession :exec
-- id + user_id so a delete can never reach another person's session even if
-- the ownership check above it were ever bypassed.
DELETE FROM assistant_session
WHERE id = $1 AND user_id = $2;

-- name: CreateAssistantMessage :one
INSERT INTO assistant_message (
    session_id, role, content, tool_calls, tool_call_id, tool_name, tool_result
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListAssistantMessages :many
-- Monotonic identity order preserves causal order even when timestamps tie.
SELECT * FROM assistant_message
WHERE session_id = $1
ORDER BY sequence ASC;

-- name: ListRecentAssistantMessages :many
-- The LLM context window: newest first so the caller takes the last N and
-- reverses. Sequence is the causal order of inserts.
SELECT * FROM assistant_message
WHERE session_id = $1
ORDER BY sequence DESC
LIMIT $2;

-- name: CreateAssistantPendingOperation :one
-- The PRE-AUTHORIZATION record. Written before any mutation, from target data
-- the tool has already resolved, so `summary` names exactly what a confirm
-- would do. See migration 198 for why this is not assistant_operation.
INSERT INTO assistant_pending_operation (
    run_id, session_id, user_id, tool_name, arguments, summary, workspace_id, target
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetAssistantPendingOperation :one
SELECT * FROM assistant_pending_operation
WHERE id = $1;

-- name: ListAssistantPendingOperations :many
SELECT * FROM assistant_pending_operation
WHERE session_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: ResolveAssistantPendingOperation :one
-- Compare-and-swap out of 'pending'. This is what makes a confirmation
-- single-use: the second confirm of the same operation matches no row and the
-- caller answers 409. Expiry is evaluated here rather than by a sweeper, so an
-- operation that has sat too long can never be claimed.
UPDATE assistant_pending_operation
SET status = sqlc.arg('status'),
    resolved_at = now()
WHERE id = sqlc.arg('id')
  AND status = 'pending'
  AND expires_at > now()
RETURNING *;

-- name: ExpireAssistantPendingOperation :exec
-- Read-time expiry: a pending operation past its deadline is recorded as
-- expired the moment somebody looks at it, so the row stops lying.
UPDATE assistant_pending_operation
SET status = 'expired', resolved_at = now()
WHERE id = $1 AND status = 'pending' AND expires_at <= now();
