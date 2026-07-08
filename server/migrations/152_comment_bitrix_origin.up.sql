-- Per-comment Bitrix-origin marker. A comment imported from a Bitrix task
-- (importBitrixComments) stores the source Bitrix comment id here; a NULL means
-- the comment originated in Agora (a member/agent/system comment). This is the
-- reliable signal the issue-detail activity tabs use to separate Bitrix comments
-- from in-Agora discussion — the issue-level bitrix_synced_comment_ids array
-- holds Bitrix ids for dedup and cannot be matched to an Agora comment row.
-- Nullable + no default so every existing comment reads as Agora-origin.
ALTER TABLE comment ADD COLUMN bitrix_comment_id text;

-- Partial index: only Bitrix-origin rows are indexed, keeping it tiny while
-- making "is this Bitrix comment already imported" lookups cheap if we later
-- move dedup onto the column.
CREATE INDEX idx_comment_bitrix_comment_id
    ON comment (bitrix_comment_id)
    WHERE bitrix_comment_id IS NOT NULL;
