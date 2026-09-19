-- 'duplicate' rows would violate the narrower CHECK; downgrade them to the
-- value the pre-import vocabulary had for "some relation exists".
UPDATE issue_dependency SET type = 'related' WHERE type = 'duplicate';
ALTER TABLE issue_dependency DROP CONSTRAINT IF EXISTS issue_dependency_type_check;
ALTER TABLE issue_dependency ADD CONSTRAINT issue_dependency_type_check
    CHECK (type IN ('blocks', 'blocked_by', 'related'));

DROP INDEX IF EXISTS idx_issue_external_ref;
DROP INDEX IF EXISTS uq_comment_external_ref;
ALTER TABLE comment DROP CONSTRAINT IF EXISTS comment_external_ref_complete;
ALTER TABLE comment DROP COLUMN IF EXISTS external_id;
ALTER TABLE comment DROP COLUMN IF EXISTS external_source;
