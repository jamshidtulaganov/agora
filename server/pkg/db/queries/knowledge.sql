-- name: CreateKnowledgeDoc :one
INSERT INTO knowledge_doc (
    workspace_id, title, source, attachment_id, filename, content_type, size_bytes, note_body, created_by
) VALUES (
    @workspace_id, @title, @source, sqlc.narg('attachment_id'), @filename, @content_type, @size_bytes, @note_body, sqlc.narg('created_by')
)
RETURNING *;

-- name: GetKnowledgeDoc :one
SELECT * FROM knowledge_doc
WHERE id = $1 AND workspace_id = $2 AND archived_at IS NULL;

-- name: ListKnowledgeDocs :many
-- Pinned first, then most recently changed. created_by_name for the list.
SELECT d.*, COALESCE(u.name, '')::text AS created_by_name
FROM knowledge_doc d
LEFT JOIN "user" u ON u.id = d.created_by
WHERE d.workspace_id = $1 AND d.archived_at IS NULL
ORDER BY d.pinned DESC, d.updated_at DESC;

-- name: UpdateKnowledgeDocMeta :one
UPDATE knowledge_doc SET
    title = COALESCE(sqlc.narg('title'), title),
    pinned = COALESCE(sqlc.narg('pinned'), pinned),
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND archived_at IS NULL
RETURNING *;

-- name: ArchiveKnowledgeDoc :execrows
UPDATE knowledge_doc SET archived_at = now(), updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND archived_at IS NULL;

-- name: ClaimKnowledgeDocForProcessing :one
-- Single worker per document: only a processing doc nobody holds (or whose
-- holder died more than 10 minutes ago) is claimed.
UPDATE knowledge_doc SET processing_started_at = now()
WHERE id = $1 AND status = 'processing' AND archived_at IS NULL
  AND (processing_started_at IS NULL OR processing_started_at < now() - interval '10 minutes')
RETURNING *;

-- name: ListStaleProcessingKnowledgeDocs :many
-- Documents a crashed or restarted server left half-read.
SELECT id FROM knowledge_doc
WHERE status = 'processing' AND archived_at IS NULL
  AND (processing_started_at IS NULL OR processing_started_at < now() - interval '10 minutes')
ORDER BY created_at
LIMIT 50;

-- name: FinishKnowledgeDoc :one
UPDATE knowledge_doc SET
    status = @status,
    error = @error,
    page_count = @page_count,
    char_count = @char_count,
    chunk_count = @chunk_count,
    processing_started_at = NULL,
    updated_at = now()
WHERE id = @id
RETURNING *;

-- name: RequeueKnowledgeDoc :one
UPDATE knowledge_doc SET status = 'processing', error = '', processing_started_at = NULL, updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND archived_at IS NULL
RETURNING *;

-- name: DeleteKnowledgeChunksForDoc :exec
DELETE FROM knowledge_chunk WHERE doc_id = $1;

-- name: InsertKnowledgeChunks :copyfrom
INSERT INTO knowledge_chunk (doc_id, workspace_id, ord, heading_path, location, body)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListKnowledgeChunks :many
SELECT id, doc_id, ord, heading_path, location, body
FROM knowledge_chunk
WHERE doc_id = @doc_id AND ord >= @from_ord
ORDER BY ord
LIMIT @max_chunks;

-- name: ListPinnedKnowledgeChunks :many
-- Text of the workspace's "always include" documents, for prompts and
-- briefs; the caller applies the character budget.
SELECT d.title AS doc_title, c.ord, c.heading_path, c.location, c.body
FROM knowledge_chunk c
JOIN knowledge_doc d ON d.id = c.doc_id
WHERE c.workspace_id = $1 AND d.pinned AND d.status = 'ready' AND d.archived_at IS NULL
ORDER BY d.updated_at DESC, c.ord
LIMIT 200;

-- name: InsertKnowledgeSearchLog :exec
INSERT INTO knowledge_search_log (workspace_id, user_id, source, query, result_count, top_doc_id)
VALUES (@workspace_id, sqlc.narg('user_id'), @source, @query, @result_count, sqlc.narg('top_doc_id'));
