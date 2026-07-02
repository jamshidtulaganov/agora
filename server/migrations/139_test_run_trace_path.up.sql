-- 139_test_run_trace_path.up.sql
-- Playwright trace: a run_test_cases execution MAY capture a full Playwright
-- trace (DOM snapshots + screenshots + sources per step) as a .zip on the
-- agent's runtime box. The path is local to the daemon that produced it; the
-- backend launches `playwright show-trace` on that daemon and reverse-proxies
-- the viewer so a QA reviewer can time-travel the run in-app. Additive +
-- defaulted, so a trace-less run behaves exactly as before. Plain text, no
-- CHECK (matches the output/script columns).
ALTER TABLE test_run ADD COLUMN IF NOT EXISTS trace_path text NOT NULL DEFAULT '';
