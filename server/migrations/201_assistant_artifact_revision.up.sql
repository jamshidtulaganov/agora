-- Immutable revision history for assistant artifacts
-- (docs/agora-assistant-final-plan.md §4 Phase 4: "Add immutable revision
-- storage and concurrency checks; incrementing a version number alone is
-- insufficient").
--
-- Until now an artifact carried a `version` integer that update_artifact
-- incremented while OVERWRITING the body. The number said a change happened
-- and nothing more: v3 was not inspectable once v4 existed, so a refreshed
-- report could not be compared with the one a decision was made from, and a
-- bad regeneration was unrecoverable. A version picker over that is a picker
-- with one entry.
--
-- The split this table makes:
--
--   assistant_artifact          — the CURRENT pointer (what the pane renders)
--   assistant_artifact_revision — APPEND-ONLY history (what every version was)
--
-- Every version the artifact ever held has exactly one row here, including the
-- current one, so "show me v3" is a read rather than a reconstruction and the
-- current state is never a special case for the reader.
--
-- UNIQUE (artifact_id, version) is the concurrency backstop, not decoration.
-- The write path bumps the pointer with UPDATE ... SET version = version + 1
-- RETURNING and inserts the returned number here IN THE SAME TRANSACTION, so
-- the row lock taken by the UPDATE serializes concurrent updaters and each one
-- necessarily claims a distinct version. If a duplicate ever does reach this
-- index, the history and the pointer disagree — the transaction aborts and the
-- caller is told, rather than a second body being filed under a version number
-- that already means something else.
--
-- No user_id/session_id column: authorization is the artifact's, resolved
-- through artifact -> session -> user_id exactly as it is for the body. A
-- denormalized owner here would be a second answer to a question that already
-- has one.
CREATE TABLE assistant_artifact_revision (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_id UUID NOT NULL REFERENCES assistant_artifact(id) ON DELETE CASCADE,
    version INT NOT NULL,
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (artifact_id, version)
);

-- The version picker's read: newest first, one artifact at a time. The UNIQUE
-- constraint's index already covers (artifact_id, version) for lookups of a
-- single version, and Postgres can walk it backwards for the list, so no
-- second index is created here.

-- Backfill: every artifact that already exists gets the revision row for the
-- state it is in right now, at its current version number. Without this, an
-- artifact made yesterday would open a picker that shows nothing while the
-- pane renders v4 — history that starts empty is worse than no history,
-- because it reads as "the earlier versions were lost".
--
-- created_at is the artifact's updated_at, the honest timestamp for when the
-- CURRENT body came to be. Intermediate versions are genuinely unrecoverable
-- (the bodies were overwritten), so the backfill claims only what is true: one
-- row, the version the pointer says, the moment it was written.
INSERT INTO assistant_artifact_revision (artifact_id, version, title, content, created_at)
SELECT id, version, title, content, updated_at
FROM assistant_artifact
ON CONFLICT (artifact_id, version) DO NOTHING;
