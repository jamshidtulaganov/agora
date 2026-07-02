-- 138_test_case_script.up.sql
-- Compiled QA script: an automated ([e2e]/[api]) case MAY carry a self-contained
-- runnable Playwright ESM script (authored ONCE by an agent). run_test_cases then
-- EXECUTES it deterministically (node <tmp>.mjs, exit code = pass/fail) instead of
-- driving the browser action-by-action. Additive + defaulted, so a script-less
-- case behaves exactly as before. Plain text, no CHECK (matches steps/expected).
ALTER TABLE test_case ADD COLUMN IF NOT EXISTS script text NOT NULL DEFAULT '';
