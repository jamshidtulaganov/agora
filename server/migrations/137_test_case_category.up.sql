-- A senior-QA-style split: is this case exercising the golden/valid path
-- (positive), or an invalid/boundary/adversarial input the system must
-- reject or degrade gracefully on (negative)? Plain text, no CHECK
-- constraint — enum drift downgrades, never crashes, matching kind/source.
ALTER TABLE test_case ADD COLUMN IF NOT EXISTS category text NOT NULL DEFAULT 'positive';
