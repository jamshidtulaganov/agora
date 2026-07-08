DROP INDEX IF EXISTS idx_issue_active;
ALTER TABLE issue DROP COLUMN IF EXISTS archived_at;
