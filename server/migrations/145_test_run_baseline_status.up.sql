-- Baseline discrimination: whether a test also FAILED on the pre-change baseline
-- (fail-before/pass-after). A test that PASSES on both baseline and branch is
-- non-discriminating and provides no evidence for qa:pass. Additive + defaulted;
-- 'unknown' is the fail-safe (legacy rows + discrimination-flag-off runs stay
-- neutral, never counted as discriminating).
ALTER TABLE test_run ADD COLUMN IF NOT EXISTS baseline_status text NOT NULL DEFAULT 'unknown';
