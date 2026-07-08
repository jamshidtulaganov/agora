DROP INDEX IF EXISTS idx_comment_bitrix_comment_id;
ALTER TABLE comment DROP COLUMN IF EXISTS bitrix_comment_id;
