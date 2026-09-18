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

-- ---------------------------------------------------------------------------
-- Pins (published reports) — migration 203
-- ---------------------------------------------------------------------------

-- name: CreateAssistantArtifactPin :one
-- workspace_id is written by the caller from the PROJECT it resolved, never
-- from a workspace the request named: the pin's tenant and its target must
-- agree by construction, or a report could be published into a workspace the
-- project does not belong to.
INSERT INTO assistant_artifact_pin (artifact_id, workspace_id, project_id, pinned_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetAssistantArtifactPin :one
-- By id alone, like GetAssistantArtifact: the two authorization paths a pin
-- has (its artifact's owner, or an admin of its workspace) are decided at the
-- boundary from the columns this returns. A query that pre-filtered by one of
-- them would make the other unrepresentable.
SELECT * FROM assistant_artifact_pin
WHERE id = $1;

-- name: GetAssistantArtifactPinForArtifactAndProject :one
-- The idempotency read: what the UNIQUE (artifact_id, project_id) conflict
-- already holds, so a repeated pin answers with the pin that exists.
SELECT * FROM assistant_artifact_pin
WHERE artifact_id = $1 AND project_id = $2;

-- name: DeleteAssistantArtifactPin :exec
-- Keyed on the pin id the boundary already resolved and authorized. Deleting
-- a pin revokes the read grant and touches no artifact.
DELETE FROM assistant_artifact_pin
WHERE id = $1;

-- name: ListAssistantArtifactPinsByProject :many
-- The project page's Reports section. Joined to the artifact for what the row
-- displays (title / kind / version / last change) and to "user" twice for the
-- two people a reader needs named: who WROTE the report (its owner, the only
-- one who can revise it) and who PUBLISHED it here.
--
-- Deliberately WITHOUT content, for the same reason the artifact list is: a
-- body is up to 256 KB and this draws a list of cards. The content is the
-- single-report read's job.
--
-- workspace_id is in the predicate as well as project_id — a project id is
-- enough to find the rows, but scoping every read by tenant is what keeps a
-- mis-scoped project id from ever returning another workspace's reports.
SELECT p.id, p.artifact_id, p.workspace_id, p.project_id, p.pinned_by, p.created_at,
       a.title, a.kind, a.version, a.updated_at,
       a.user_id                AS owner_id,
       owner_u.name             AS owner_name,
       pinner_u.name            AS pinned_by_name
FROM assistant_artifact_pin p
JOIN assistant_artifact a ON a.id = p.artifact_id
JOIN "user" owner_u ON owner_u.id = a.user_id
JOIN "user" pinner_u ON pinner_u.id = p.pinned_by
WHERE p.workspace_id = $1 AND p.project_id = $2
ORDER BY p.created_at DESC, p.id DESC;

-- name: GetAssistantArtifactPinWithArtifact :one
-- The single-report read: the list row plus the CURRENT body. session_id is
-- not selected — a reader reached through a pin is not entitled to the
-- conversation that produced the report, and a column that is never selected
-- cannot be leaked by a later change to the response struct.
SELECT p.id, p.artifact_id, p.workspace_id, p.project_id, p.pinned_by, p.created_at,
       a.title, a.kind, a.version, a.updated_at, a.content,
       a.user_id                AS owner_id,
       owner_u.name             AS owner_name,
       pinner_u.name            AS pinned_by_name
FROM assistant_artifact_pin p
JOIN assistant_artifact a ON a.id = p.artifact_id
JOIN "user" owner_u ON owner_u.id = a.user_id
JOIN "user" pinner_u ON pinner_u.id = p.pinned_by
WHERE p.id = $1;

-- name: ListAssistantArtifactPinsByArtifact :many
-- "Which workspaces must hear that this report changed?" — read on the update
-- path so a refreshed artifact invalidates every project page that publishes
-- it. Covered by the UNIQUE index's leading artifact_id column.
SELECT * FROM assistant_artifact_pin
WHERE artifact_id = $1
ORDER BY created_at ASC;

-- ---------------------------------------------------------------------------
-- Report schedules (scheduled refresh) — migration 204
-- ---------------------------------------------------------------------------

-- name: UpsertAssistantReportSchedule :one
-- Create-or-replace, expressible only because UNIQUE (pin_id) says a published
-- report has exactly one cadence. The endpoint is a PUT and behaves like one:
-- sending a new cadence for a pin that already has one REPLACES it in place
-- rather than opening a second unattended run against the same artifact.
--
-- created_by and created_at are deliberately NOT touched on conflict: they
-- record who first put this report on a standing schedule, which is the
-- accountability question a workspace asks about unattended spend. next_run_at
-- is always rewritten, because the new cadence's next slot has nothing to do
-- with the old one's.
INSERT INTO assistant_report_schedule (
    pin_id, frequency, at_time, weekday, timezone, created_by, next_run_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (pin_id) DO UPDATE
SET frequency   = EXCLUDED.frequency,
    at_time     = EXCLUDED.at_time,
    weekday     = EXCLUDED.weekday,
    timezone    = EXCLUDED.timezone,
    enabled     = true,
    next_run_at = EXCLUDED.next_run_at,
    -- The outcome of the PREVIOUS cadence describes a schedule that no longer
    -- exists, so it is cleared rather than left to be read as this one's.
    last_status = '',
    last_error  = ''
RETURNING *;

-- name: DeleteAssistantReportSchedule :execrows
-- Keyed on the PIN, not the schedule id: the endpoint addresses the pin
-- (.../pins/{pinId}/schedule) and the boundary has already authorized it.
-- :execrows so the caller can tell "there was a schedule and it is gone" from
-- "there was nothing to delete" — a DELETE of a missing schedule is a 204
-- either way, but only the first is a change worth telling the workspace about.
DELETE FROM assistant_report_schedule
WHERE pin_id = $1;

-- name: GetAssistantReportScheduleByPin :one
-- The single-report read's one extra query. By pin id, because that is what
-- every authorization path here already holds.
SELECT * FROM assistant_report_schedule
WHERE pin_id = $1;

-- name: ListAssistantReportSchedulesByProject :many
-- The project page's schedules, in ONE query rather than one per row: the list
-- endpoint fetches its pins and then this, and joins the two in Go. A LEFT JOIN
-- on the pin list would have been the other option, but it would turn every
-- column of that list into a nullable one for the sake of a table most
-- installs have no rows in.
--
-- workspace_id is in the predicate beside project_id for the same reason it is
-- in the pin list: every read here is scoped by tenant, so a mis-scoped
-- project id can never surface another workspace's cadence.
SELECT s.* FROM assistant_report_schedule s
JOIN assistant_artifact_pin p ON p.id = s.pin_id
WHERE p.workspace_id = $1 AND p.project_id = $2;

-- name: ListDueAssistantReportSchedules :many
-- The ticker's candidates. Deliberately a plain read that CLAIMS NOTHING —
-- the claim is the next query, one row at a time.
--
-- Why not one UPDATE ... RETURNING for the whole batch: the value each row
-- must be advanced TO is different per row and is computed from presets, a
-- wall-clock time and an IANA timezone. Expressing that in SQL would mean a
-- second implementation of the schedule math living next to the Go one, and
-- the two would drift on exactly the cases that are hard to get right (DST,
-- weekend skipping). One implementation, in Go, table-tested — and SQL does
-- the part it is actually better at: an atomic compare-and-swap per row.
--
-- LIMIT bounds one tick's work: schedules are few by construction (one per
-- published report, at most one run a day each), and a tick that tried to
-- start a thousand assistant runs would be the bug, not the fix.
SELECT * FROM assistant_report_schedule
WHERE enabled AND next_run_at <= now()
ORDER BY next_run_at ASC
LIMIT $1;

-- name: ClaimDueAssistantReportSchedule :one
-- The claim: advance the slot BEFORE the run, and only if this row is still
-- sitting on the slot the caller read. That `next_run_at = claimed_slot`
-- predicate is the whole concurrency story — it is a compare-and-swap, so two
-- server processes that both listed the same due row have exactly one winner
-- (the loser matches no rows and skips), and a run is at-most-once per slot.
--
-- No transaction and no FOR UPDATE SKIP LOCKED is needed for that: a single
-- UPDATE is already atomic, and the CAS predicate carries the same guarantee
-- with none of the lock-holding-across-a-model-call that the locking shape
-- would invite.
--
-- last_status = 'running' is written here rather than after the run starts, so
-- a row that was claimed and then lost to a crash reads as what it is.
UPDATE assistant_report_schedule
SET next_run_at = sqlc.arg('next_run_at'),
    last_status = 'running'
WHERE id = sqlc.arg('id')
  AND enabled
  AND next_run_at = sqlc.arg('claimed_slot')
RETURNING *;

-- name: UpdateAssistantReportScheduleOutcome :exec
-- What the project page's freshness badge reads. last_run_at moves on every
-- attempt, including a skipped one: "we looked at this slot and chose not to
-- run" is a fact about freshness the reader needs as much as a failure is.
UPDATE assistant_report_schedule
SET last_run_at = now(),
    last_status = sqlc.arg('last_status'),
    last_error  = sqlc.arg('last_error')
WHERE id = sqlc.arg('id');
