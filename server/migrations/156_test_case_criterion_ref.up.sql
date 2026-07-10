-- Traceability: which acceptance criterion / requirement a test case verifies
-- (QA lens phase 3). A short free-text pointer — "AC2" (matching the numbered
-- criteria the QA plan context hands agents) or a trimmed quote of the
-- criterion — NOT a foreign key: issue.acceptance_criteria is importer-written
-- JSONB with no stable per-criterion ids to reference. '' = untraced (all
-- existing rows). The dedicated `source` column is origin (human|agent|
-- promoted) and stays untouched.
ALTER TABLE test_case
    ADD COLUMN criterion_ref text NOT NULL DEFAULT '';
