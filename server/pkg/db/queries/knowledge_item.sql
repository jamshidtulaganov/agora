-- Knowledge items — structured KB (see migration 146).

-- name: UpsertKnowledgeItem :one
-- Trusted-proposer insert (synthesizer or human): an exact normalized-title
-- collision with a live item CONFIRMS it (hits+1, last_confirmed_at) and
-- leaves title/body/status untouched. `(xmax = 0)` tells the caller whether
-- a row was inserted.
INSERT INTO knowledge_item (
    workspace_id, project_id, kb_name, module, kind, title, body, norm_title,
    source_issue_id, created_by_type, created_by_id, status
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (workspace_id, kb_name, norm_title) WHERE status <> 'archived'
DO UPDATE SET
    hits = knowledge_item.hits + 1,
    last_confirmed_at = now(),
    updated_at = now()
RETURNING *, (xmax = 0) AS inserted;

-- name: InsertKnowledgeItemIgnoreDup :one
-- Untrusted-proposer insert (any non-synthesizer agent): an exact collision
-- is a silent no-op — untrusted restatements must NOT bump rank (rank-pumping
-- guard). Caller checks rows-affected via the returned id.
INSERT INTO knowledge_item (
    workspace_id, project_id, kb_name, module, kind, title, body, norm_title,
    source_issue_id, created_by_type, created_by_id, status
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (workspace_id, kb_name, norm_title) WHERE status <> 'archived'
DO NOTHING
RETURNING id;

-- name: ListKnowledgeItemsByProject :many
-- Review/list endpoint (project-scoped view). Optional status filter;
-- archived excluded unless explicitly requested.
SELECT * FROM knowledge_item
WHERE workspace_id = $1 AND project_id = $2
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
  AND (sqlc.narg('status')::text IS NOT NULL OR status <> 'archived')
ORDER BY status ASC, created_at DESC, id ASC;

-- name: ListActiveKnowledgeItemsForCompile :many
-- Compile input, keyed by KB name. Order IS the ranking: confirmed-often
-- first, then most recently confirmed/created. Fully derived from stored
-- columns so a recompile is a pure function of the rows (deterministic).
SELECT * FROM knowledge_item
WHERE workspace_id = $1 AND kb_name = $2 AND status = 'active'
ORDER BY hits DESC, COALESCE(last_confirmed_at, created_at) DESC, created_at DESC, id ASC;

-- name: ListKnowledgeItemKeysForDedupe :many
-- Near-duplicate scan input: only the columns the Jaccard pass needs.
SELECT id, norm_title, kind, status FROM knowledge_item
WHERE workspace_id = $1 AND kb_name = $2 AND status <> 'archived';

-- name: CountProposedAgentKnowledgeItems :one
-- Spam guard input: how many agent-proposed rows already sit unreviewed.
SELECT COUNT(*) FROM knowledge_item
WHERE workspace_id = $1 AND kb_name = $2 AND status = 'proposed' AND created_by_type = 'agent';

-- name: GetKnowledgeItem :one
SELECT * FROM knowledge_item
WHERE id = $1 AND workspace_id = $2;

-- name: UpdateKnowledgeItem :one
-- COALESCE on EVERY mutable column — do NOT repeat the UpdateProject footgun
-- (partial param structs must not NULL sibling fields).
UPDATE knowledge_item SET
    module = COALESCE(sqlc.narg('module'), module),
    kind = COALESCE(sqlc.narg('kind'), kind),
    title = COALESCE(sqlc.narg('title'), title),
    body = COALESCE(sqlc.narg('body'), body),
    norm_title = COALESCE(sqlc.narg('norm_title'), norm_title),
    status = COALESCE(sqlc.narg('status'), status),
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: BumpKnowledgeItemHits :exec
UPDATE knowledge_item SET hits = hits + 1, last_confirmed_at = now(), updated_at = now()
WHERE id = $1 AND workspace_id = $2;

-- name: ReassignKnowledgeItemsKBName :exec
-- Project rename / kb_skill override change: keep the compile key honest.
UPDATE knowledge_item SET kb_name = $3, updated_at = now()
WHERE workspace_id = $1 AND project_id = $2;

-- name: DeleteKnowledgeItem :execrows
-- :execrows so the handler can 404 on 0 rows (convention from #1661).
DELETE FROM knowledge_item WHERE id = $1 AND workspace_id = $2;
