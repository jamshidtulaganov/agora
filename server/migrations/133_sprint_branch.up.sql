-- A sprint's shared integration branch (the real git branch all its tasks commit
-- to, e.g. "billing" or a per-sprint "sprint-9"). QA deploys + smokes THIS branch
-- on the project's sprint box each tier (per-task / daily / sprint-end). Set per
-- sprint by the team; empty falls back to the sprint/<id> convention in code.
ALTER TABLE sprint ADD COLUMN branch text NOT NULL DEFAULT '';
