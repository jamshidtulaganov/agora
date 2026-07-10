ALTER TABLE test_run
    DROP COLUMN IF EXISTS commit_sha,
    DROP COLUMN IF EXISTS session_id,
    DROP COLUMN IF EXISTS started_at,
    DROP COLUMN IF EXISTS finished_at;

ALTER TABLE qa_evidence
    DROP COLUMN IF EXISTS commit_sha,
    DROP COLUMN IF EXISTS triggered_by,
    DROP COLUMN IF EXISTS started_at,
    DROP COLUMN IF EXISTS finished_at;
