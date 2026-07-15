ALTER TABLE orchestration_step
    DROP COLUMN IF EXISTS conflict_files,
    DROP COLUMN IF EXISTS merge_status,
    DROP COLUMN IF EXISTS head_sha,
    DROP COLUMN IF EXISTS base_sha,
    DROP COLUMN IF EXISTS worktree_branch;
