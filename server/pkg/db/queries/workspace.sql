-- name: ListWorkspaces :many
-- w.* keeps the row shape identical to the workspace model so sqlc reuses
-- db.Workspace. default_mcp_config rides along but is never mapped into
-- WorkspaceResponse (see workspaceToResponse) — the generic workspace
-- resource must not expose it.
SELECT w.*
FROM member m
JOIN workspace w ON w.id = m.workspace_id
WHERE m.user_id = $1
ORDER BY w.created_at ASC;

-- name: GetWorkspace :one
SELECT * FROM workspace
WHERE id = $1;

-- name: GetWorkspaceBySlug :one
SELECT * FROM workspace
WHERE slug = $1;

-- name: CreateWorkspace :one
INSERT INTO workspace (name, slug, description, context, issue_prefix)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateWorkspace :one
UPDATE workspace SET
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    context = COALESCE(sqlc.narg('context'), context),
    settings = COALESCE(sqlc.narg('settings'), settings),
    repos = COALESCE(sqlc.narg('repos'), repos),
    issue_prefix = COALESCE(sqlc.narg('issue_prefix'), issue_prefix),
    avatar_url = COALESCE(sqlc.narg('avatar_url'), avatar_url),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: IncrementIssueCounter :one
-- Hand out the next free issue number for a workspace. We take GREATEST of the
-- naive counter+1 and (max existing number)+1 so the counter self-heals when it
-- lags behind a number already present in the issue table. That drift happens
-- when issues land with numbers the counter never advanced past — e.g. a bulk
-- data load / DB restore that preserved external (Bitrix) numbering. Without the
-- GREATEST, a lagging counter hands out a number that already exists and every
-- CreateIssue fails on uq_issue_workspace_number (SQLSTATE 23505) until the
-- counter is manually bumped (sd-main hit this: counter 179 vs max number 319).
-- The UPDATE row-lock on `workspace` serializes concurrent creates in the same
-- workspace, so the MAX subquery never races a sibling insert; the unique
-- constraint remains the ultimate backstop. The (workspace_id, number) index
-- makes the MAX a cheap reverse index scan.
UPDATE workspace SET issue_counter = GREATEST(
        issue_counter + 1,
        COALESCE((SELECT MAX(number) FROM issue WHERE workspace_id = $1), 0) + 1
    )
WHERE id = $1
RETURNING issue_counter;

-- name: DeleteWorkspace :exec
DELETE FROM workspace WHERE id = $1;

-- name: GetWorkspaceDefaultMcpConfig :one
SELECT default_mcp_config FROM workspace
WHERE id = $1;

-- name: UpdateWorkspaceDefaultMcpConfig :one
UPDATE workspace SET default_mcp_config = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ClearWorkspaceDefaultMcpConfig :one
UPDATE workspace SET default_mcp_config = NULL, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MergeWorkspaceSettingsEntry :exec
-- Atomically merge one or more top-level keys into workspace.settings via
-- jsonb || so concurrent writers never clobber sibling keys — unlike
-- UpdateWorkspace's whole-blob settings replace (same rationale as
-- MergeProjectCoverageEntry). First user: stamping kb_synthesizer_agent_id.
UPDATE workspace SET
    settings = COALESCE(settings, '{}'::jsonb) || sqlc.arg('entry')::jsonb,
    updated_at = now()
WHERE id = sqlc.arg('id');

-- name: SetWorkspaceSettingKey :one
-- KEY-SCOPED write of a single workspace.settings key via jsonb_set — the same
-- clobber-safe pattern as SetProjectSettingKey. Used for the workspace-level
-- design manifest (the shared design system projects inherit).
UPDATE workspace SET
    settings = jsonb_set(COALESCE(settings, '{}'::jsonb), ARRAY[sqlc.arg('key')::text], sqlc.arg('value')::jsonb, true),
    updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING *;
