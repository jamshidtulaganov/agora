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
--
-- Two things make this the concurrency primitive rather than just a write:
--
--   version = version + 1 RETURNING — the new number is computed by the
--   database from the row it locked, never from a number the caller read
--   earlier. Concurrent updaters queue on that row lock and each one leaves
--   with a distinct version, which is what lets the revision insert in the
--   same transaction use the returned number as a key.
--
--   expected_version — optional compare-and-swap. When NULL the update is
--   unconditional (the normal "make this change" case). When supplied, the
--   statement matches NOTHING unless the artifact is still at that version,
--   so an update written against a body the caller read is refused rather
--   than silently overwriting a newer one. The guard lives HERE, in the same
--   statement as the bump, because a check done before the UPDATE would be a
--   read the next writer can invalidate before the write lands.
UPDATE assistant_artifact
SET content    = sqlc.arg('content'),
    title      = COALESCE(sqlc.narg('title'), title),
    version    = version + 1,
    updated_at = now()
WHERE id = sqlc.arg('id')
  AND (sqlc.narg('expected_version')::int IS NULL
       OR version = sqlc.narg('expected_version')::int)
RETURNING *;

-- name: CountAssistantArtifactsBySession :one
-- Backs the per-session cap. Cheap: the session index covers it and a session
-- holds at most a couple of dozen rows by construction.
SELECT COUNT(*) FROM assistant_artifact
WHERE session_id = $1;

-- name: CreateAssistantArtifactRevision :one
-- Appends one immutable revision. The version is supplied by the caller — it
-- is the number the artifact UPDATE just returned, inside the same
-- transaction, so the pointer and the history can never name different
-- versions for the same body. Never call this with a version the artifact row
-- does not hold.
INSERT INTO assistant_artifact_revision (artifact_id, version, title, content)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListAssistantArtifactRevisions :many
-- The version picker's list: NEWEST FIRST, deliberately WITHOUT content. Same
-- reasoning as the artifact list — a body is up to 256 KB and up to 50 of them
-- per artifact, while the picker draws a version number, a title and a date.
SELECT id, artifact_id, version, title, created_at
FROM assistant_artifact_revision
WHERE artifact_id = $1
ORDER BY version DESC;

-- name: GetAssistantArtifactRevision :one
-- One historical version in full. Keyed by (artifact_id, version) rather than
-- by revision id: the picker knows "v3 of this artifact", and routing through
-- the artifact keeps the ownership chain (artifact -> session -> user) as the
-- single authorization path.
SELECT * FROM assistant_artifact_revision
WHERE artifact_id = $1 AND version = $2;

-- name: CountAssistantArtifactRevisions :one
SELECT COUNT(*) FROM assistant_artifact_revision
WHERE artifact_id = $1;

-- name: TrimAssistantArtifactRevisions :exec
-- Retention cap, applied on append inside the same transaction.
--
-- v1 is PINNED: it is the only version whose meaning does not depend on
-- another ("what was this before anyone edited it"), and losing it turns the
-- oldest surviving revision into a silent lie about where the artifact
-- started. So the cap keeps v1 plus the `keep` newest later revisions, and
-- deletes the middle — the versions with a surviving neighbour on both sides,
-- which are the cheapest to lose.
DELETE FROM assistant_artifact_revision AS doomed
WHERE doomed.artifact_id = sqlc.arg('artifact_id')
  AND doomed.version > 1
  AND doomed.id NOT IN (
      SELECT kept.id FROM assistant_artifact_revision AS kept
      WHERE kept.artifact_id = sqlc.arg('artifact_id')
        AND kept.version > 1
      ORDER BY kept.version DESC
      LIMIT sqlc.arg('keep_newest')
  );
