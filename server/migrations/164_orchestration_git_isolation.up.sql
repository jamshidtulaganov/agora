ALTER TABLE orchestration_step
    ADD COLUMN worktree_branch TEXT,
    ADD COLUMN base_sha TEXT,
    ADD COLUMN head_sha TEXT,
    ADD COLUMN merge_status TEXT NOT NULL DEFAULT 'not_checked'
        CHECK (merge_status IN ('not_checked', 'clean', 'conflicts', 'uncommitted', 'unavailable')),
    ADD COLUMN conflict_files JSONB NOT NULL DEFAULT '[]';
