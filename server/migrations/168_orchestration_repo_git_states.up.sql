-- Per-repository git evidence for orchestration steps. A local_directory
-- parent may contain several repos; the single worktree_branch/base_sha/
-- head_sha columns can only describe one of them, which made the integration
-- gate blind to every repo after the first. git_states stores one entry per
-- repo: [{repo, branch, base_sha, head_sha, merge_status, conflict_files}].
-- The legacy single-value columns remain as the primary-repo summary for
-- older daemons and existing readers.
ALTER TABLE orchestration_step
    ADD COLUMN git_states JSONB NOT NULL DEFAULT '[]';
