-- name: CreateAssistantArtifact :one
-- user_id is written from the SESSION's owner, never from a caller-supplied
-- string, so the denormalized column can never disagree with the join.
INSERT INTO assistant_artifact (session_id, user_id, title, kind, content)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetAssistantArtifact :one
-- By id alone. Ownership is resolved by the caller through the session
-- (artifact -> session -> user_id), which is the authoritative chain; a
-- query that silently filtered by user would make "not found" and "not yours"
-- indistinguishable here rather than at the boundary that has to report it.
SELECT * FROM assistant_artifact
WHERE id = $1;

-- name: ListAssistantArtifactsBySession :many
-- The session's artifact gallery. Deliberately WITHOUT content: an artifact
-- body is up to 256 KB and the list is rendered as cards, so shipping bodies
-- would make the list twenty times the size of everything it displays.
-- Production order (created_at ASC), with the id tiebreak the transcript read
-- uses, because two artifacts made in one run share a millisecond.
SELECT id, session_id, title, kind, version, created_at, updated_at
FROM assistant_artifact
WHERE session_id = $1
ORDER BY created_at ASC, id ASC;

-- name: UpdateAssistantArtifact :one
-- Content always replaces; title is optional (COALESCE narg), so "add the QA
-- numbers" keeps the name the user already sees on the card. version is
-- bumped in the same statement so a concurrent read can never observe new
-- content at the old version.
UPDATE assistant_artifact
SET content    = sqlc.arg('content'),
    title      = COALESCE(sqlc.narg('title'), title),
    version    = version + 1,
    updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: CountAssistantArtifactsBySession :one
-- Backs the per-session cap. Cheap: the session index covers it and a session
-- holds at most a couple of dozen rows by construction.
SELECT COUNT(*) FROM assistant_artifact
WHERE session_id = $1;
