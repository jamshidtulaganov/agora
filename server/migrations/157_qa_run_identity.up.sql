-- QA run identity + commit binding (Phase 3 of the QA-stage review).
--
-- test_run: every recorded case run gains
--   commit_sha  — `git rev-parse HEAD` of the checkout the run tested
--                 ('' = unreported/legacy; fail-open);
--   session_id  — one uuid minted per capture dispatch, shared by every run
--                 written from the same trigger (a human manual run mints its
--                 own), so "which runs belong to the same execution" is a
--                 column, not a timestamp heuristic;
--   started_at / finished_at — run timing when the reporter provides it.
--
-- qa_evidence: the gate verdict gains
--   commit_sha  — the sha the verdict judged. Deliberately a NEW column:
--                 baseline_ref/branch_sha already exist but form the
--                 (issue_id, baseline_ref, branch_sha) UNIQUE upsert key that
--                 gives the single-current-row overwrite semantics every
--                 Phase 1/2 surface (reconciled state, human override) relies
--                 on — writing real shas into branch_sha would fork a new row
--                 per commit and silently break that model. Those two columns
--                 stay reserved for the original per-commit-history design;
--                 commit_sha is identity METADATA on the current row.
--   triggered_by — who fired the gate: 'agent' | 'human' | 'auto'
--                  ('' = unknown/legacy);
--   started_at / finished_at — gate timing (finished_at is set at capture,
--                  started_at comes from the agent's fence when reported).
ALTER TABLE test_run
    ADD COLUMN commit_sha text NOT NULL DEFAULT '',
    ADD COLUMN session_id uuid,
    ADD COLUMN started_at timestamptz,
    ADD COLUMN finished_at timestamptz;

ALTER TABLE qa_evidence
    ADD COLUMN commit_sha text NOT NULL DEFAULT '',
    ADD COLUMN triggered_by text NOT NULL DEFAULT '',
    ADD COLUMN started_at timestamptz,
    ADD COLUMN finished_at timestamptz;
