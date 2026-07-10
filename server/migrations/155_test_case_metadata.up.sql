-- Structured test-case metadata (QA lens phase 2). Three cheap text columns so
-- a case reads like a real QA test case, not a title + two blobs:
--   preconditions — setup state the tester needs before step 1 (free text);
--   priority      — p1 | p2 | p3 (plain text: enum drift downgrades, never
--                   crashes, per the API-compat rules). Default p2 = normal.
--   modality      — ui | api | unit | manual, or '' = legacy/unspecified.
--                   Drives the QA lens live-browser gate: only issues with at
--                   least one 'ui' case (or only legacy '' cases) auto-open
--                   the embedded browser bay.
-- No backfill: existing rows keep '' / 'p2' / '' via the defaults.
ALTER TABLE test_case
    ADD COLUMN preconditions text NOT NULL DEFAULT '',
    ADD COLUMN priority      text NOT NULL DEFAULT 'p2',
    ADD COLUMN modality      text NOT NULL DEFAULT '';
