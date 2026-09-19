-- Generic external linkage, replacing the per-vendor shape.
--
-- Three changes, all in service of "a re-run upserts, never duplicates":
--
--  1. comment.external_source + external_id, with a partial UNIQUE index. This
--     is the fix for the part of the Bitrix design that does not scale: dedup
--     lived in an unbounded `bitrix_synced_comment_ids` ARRAY on the issue, so
--     every re-sync read and rewrote it and a 400-comment issue turned that
--     into a hot row. A unique index does the same job in the database.
--  2. An expression index on issue.metadata->'external_ref', the issue-side
--     upsert key, so the "have I already imported this?" lookup stays cheap at
--     50k rows.
--  3. issue_dependency.type widened to admit 'duplicate' — a real relation
--     every tracker has. Linear's fourth value ('similar') and Jira's
--     installation-defined link types downgrade to 'related' with the source's
--     own type name preserved in the linkage blob: enum drift downgrades, it
--     never crashes.
ALTER TABLE comment ADD COLUMN external_source text;
ALTER TABLE comment ADD COLUMN external_id text;

-- Half a linkage is worse than none — it reads as "imported" while being
-- un-dedupable. Both columns move together.
ALTER TABLE comment ADD CONSTRAINT comment_external_ref_complete
    CHECK ((external_source IS NULL) = (external_id IS NULL));

-- Backfill the one source that already exists. DISTINCT ON keeps the earliest
-- row per (issue, bitrix comment) so the unique index below can be built even
-- if a historical double-import left a duplicate pair behind; any loser keeps
-- its bitrix_comment_id and simply carries no generic linkage.
UPDATE comment SET external_source = 'bitrix', external_id = bitrix_comment_id
WHERE id IN (
    SELECT DISTINCT ON (issue_id, bitrix_comment_id) id
    FROM comment
    WHERE bitrix_comment_id IS NOT NULL AND bitrix_comment_id <> ''
    ORDER BY issue_id, bitrix_comment_id, created_at, id
);

-- comment.bitrix_comment_id is deliberately NOT dropped here: its readers
-- (handler comment/activity responses, the `bitrix_comment_id` API field the
-- issue activity tabs switch on) are migrated to external_source in the PR that
-- owns those surfaces. The column is redundant from this migration onward and
-- is dropped there, not left as a dual-write: the importer framework writes
-- external_source/external_id only.

-- Only externally-sourced rows are indexed, so the index stays proportional to
-- what was imported rather than to the comment table.
CREATE UNIQUE INDEX uq_comment_external_ref
    ON comment (issue_id, external_source, external_id)
    WHERE external_source IS NOT NULL;

-- Issue-side upsert key: (workspace, source, external id) out of the
-- external_ref blob on issue.metadata. Partial on the blob's presence so only
-- imported issues occupy it.
CREATE INDEX idx_issue_external_ref
    ON issue (
        workspace_id,
        ((metadata -> 'external_ref' ->> 'source')),
        ((metadata -> 'external_ref' ->> 'id'))
    )
    WHERE metadata -> 'external_ref' IS NOT NULL;

ALTER TABLE issue_dependency DROP CONSTRAINT IF EXISTS issue_dependency_type_check;
ALTER TABLE issue_dependency ADD CONSTRAINT issue_dependency_type_check
    CHECK (type IN ('blocks', 'blocked_by', 'related', 'duplicate'));
