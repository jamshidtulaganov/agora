-- Immutable per-repository base snapshot shared by every worker in an
-- orchestration run. The local daemon pins this atomically before it creates
-- the first worktree because the server cannot inspect a local_directory.
ALTER TABLE orchestration_run
    ADD COLUMN base_git_states JSONB NOT NULL DEFAULT '[]';

COMMENT ON COLUMN orchestration_run.base_git_states IS
    'Immutable run-level repository bases: [{repo, head_sha}]. First daemon proposal wins; all worker worktrees use the stored commits.';
