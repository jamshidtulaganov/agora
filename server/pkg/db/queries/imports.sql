-- Importer data layer (docs/importers-plan.md §3.5, §3.7, §3.8).
--
-- Three groups, one rule each:
--   * import_connection — the sealed credential. The listing/detail queries
--     NEVER select secret_encrypted, so no endpoint built on them can leak a
--     token; the one query that does select it is named for what it is and is
--     for server-side decryption only (the git_credential idiom, migration 132).
--   * import_job — the run, as a row. Every read is workspace-scoped.
--   * external linkage — the upsert keys that make a re-import produce
--     "created: 0, updated: N" instead of duplicates.

-- name: CreateImportConnection :one
-- Add or rotate the connection for a (workspace, source, label). Rotating a
-- token clears the probe verdict so a stale 'invalid' never outlives the
-- credential that earned it.
INSERT INTO import_connection (
    workspace_id, source, label, base_url, account_email,
    secret_encrypted, scopes, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (workspace_id, source, label) DO UPDATE SET
    base_url = EXCLUDED.base_url,
    account_email = EXCLUDED.account_email,
    secret_encrypted = EXCLUDED.secret_encrypted,
    scopes = EXCLUDED.scopes,
    probe_status = '',
    probed_at = NULL,
    updated_at = now()
RETURNING id, workspace_id, source, label, base_url, account_email, scopes,
          probe_status, probed_at, created_by, created_at, updated_at;

-- name: ListImportConnections :many
-- Metadata only — never returns secret_encrypted, so the listing endpoint
-- cannot leak a token.
SELECT id, workspace_id, source, label, base_url, account_email, scopes,
       probe_status, probed_at, created_by, created_at, updated_at
FROM import_connection
WHERE workspace_id = $1
ORDER BY source, label;

-- name: GetImportConnection :one
-- Metadata only, workspace-scoped (the tenant gate). Same no-secret rule.
SELECT id, workspace_id, source, label, base_url, account_email, scopes,
       probe_status, probed_at, created_by, created_at, updated_at
FROM import_connection
WHERE id = $1 AND workspace_id = $2;

-- name: GetImportConnectionSecret :one
-- The full row INCLUDING the sealed secret. Server-side decryption only: the
-- adapter's HTTP client. Never render this row into a response.
SELECT * FROM import_connection WHERE id = $1 AND workspace_id = $2;

-- name: UpdateImportConnectionProbe :one
-- Record what the last credential probe found: ok | invalid | unreachable.
UPDATE import_connection
SET probe_status = $3, probed_at = now(), updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING id, workspace_id, source, label, base_url, account_email, scopes,
          probe_status, probed_at, created_by, created_at, updated_at;

-- name: DeleteImportConnection :execrows
DELETE FROM import_connection WHERE id = $1 AND workspace_id = $2;

-- name: CreateImportJob :one
-- Open a run. Nothing is written to the workspace until the job reaches
-- 'running' — a dry run is a job row and a plan, and no more.
INSERT INTO import_job (workspace_id, connection_id, source, status, scope, created_by)
VALUES ($1, sqlc.narg('connection_id'), $2, $3, $4, sqlc.narg('created_by'))
RETURNING *;

-- name: GetImportJob :one
SELECT * FROM import_job WHERE id = $1 AND workspace_id = $2;

-- name: ListImportJobs :many
SELECT * FROM import_job
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: GetActiveImportJob :one
-- The check that replaces the Bitrix process-global: a second import in this
-- workspace is refused with a pointer at this row, never by cancelling it.
SELECT * FROM import_job
WHERE workspace_id = $1
  AND status IN ('pending', 'dry_run', 'awaiting_confirm', 'running')
ORDER BY created_at DESC
LIMIT 1;

-- name: SetImportJobPlan :one
-- Park the dry-run result and the mapping it was computed with. The mapping is
-- frozen into the row at confirm time, so a later settings edit cannot change
-- what the human authorized.
UPDATE import_job
SET status = $3,
    plan = sqlc.narg('plan')::jsonb,
    mapping = sqlc.narg('mapping')::jsonb,
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: StartImportJob :one
-- Claim the job for execution. The status guard makes the claim idempotent
-- under a retry and under two servers racing the same confirm: exactly one
-- caller gets the row back.
UPDATE import_job
SET status = 'running', started_at = now(), updated_at = now()
WHERE id = $1 AND workspace_id = $2
  AND status IN ('pending', 'dry_run', 'awaiting_confirm')
RETURNING *;

-- name: UpdateImportJobProgress :exec
-- Throttled progress write (every N rows or every 2s, whichever is slower).
-- Deliberately :exec — progress is published on the event bus, not read back.
UPDATE import_job
SET totals = $3, updated_at = now()
WHERE id = $1 AND workspace_id = $2;

-- name: FinishImportJob :one
-- Terminal write: done | failed | cancelled, with the receipt numbers.
UPDATE import_job
SET status = $3,
    totals = $4,
    failures = $5,
    artifact_id = sqlc.narg('artifact_id'),
    finished_at = now(),
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: FindIssueByExternalRef :one
-- The issue-side upsert key. Matches idx_issue_external_ref, predicate
-- included, so this stays an index scan at 50k imported issues.
SELECT * FROM issue
WHERE workspace_id = $1
  AND metadata -> 'external_ref' IS NOT NULL
  AND metadata -> 'external_ref' ->> 'source' = sqlc.arg('source')::text
  AND metadata -> 'external_ref' ->> 'id' = sqlc.arg('external_id')::text
LIMIT 1;

-- name: ListIssuesByExternalSource :many
-- Everything this source has already put in the workspace — the create/update
-- split the dry-run diff reports, computed in one query instead of N lookups.
SELECT id, number, metadata -> 'external_ref' ->> 'id' AS external_id
FROM issue
WHERE workspace_id = $1
  AND metadata -> 'external_ref' IS NOT NULL
  AND metadata -> 'external_ref' ->> 'source' = sqlc.arg('source')::text;

-- name: CreateIssueImported :one
-- The importer's create. Identical to CreateIssue except that created_at and
-- updated_at are explicit: a two-year backlog that all says "created today" is
-- not a migration, it is a paste. metadata carries external_ref, which is what
-- makes the next run an update. Used ONLY by the importer — every other create
-- must keep taking now().
INSERT INTO issue (
    workspace_id, title, description, status, priority,
    assignee_type, assignee_id, creator_type, creator_id,
    parent_issue_id, position, start_date, due_date, number, project_id,
    metadata, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
    $16, sqlc.arg('created_at'), sqlc.arg('updated_at')
) RETURNING *;

-- name: UpsertCommentImported :one
-- The importer's comment write, in one statement: explicit source timestamps,
-- and an upsert on the external ref so re-running an import updates an edited
-- comment instead of duplicating it. The conflict target names the partial
-- index's predicate because uq_comment_external_ref is partial.
INSERT INTO comment (
    issue_id, workspace_id, author_type, author_id, content, type, parent_id,
    external_source, external_id, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, sqlc.narg('parent_id'),
    sqlc.arg('external_source'), sqlc.arg('external_id'),
    sqlc.arg('created_at'), sqlc.arg('updated_at')
)
ON CONFLICT (issue_id, external_source, external_id) WHERE external_source IS NOT NULL
DO UPDATE SET
    content = EXCLUDED.content,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: CountIssuesByImportJob :one
-- Receipt arithmetic: how many issues this run actually landed, read back from
-- the rows themselves rather than from a counter the process held in memory.
SELECT COUNT(*) FROM issue
WHERE workspace_id = $1
  AND metadata -> 'external_ref' ->> 'import_id' = sqlc.arg('import_id')::text;
