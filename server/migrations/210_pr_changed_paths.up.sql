-- Server-derived risk tier (docs/orchestration-upgrade-plan.md §A1.1).
--
-- The tier that gates dev landing mode, auto-merge refusal, the done gate, QA
-- depth and the visual-evidence bar is today SELF-REPORTED: the agent classifies
-- its own diff against globs handed to it in the brief and writes a risk:* label.
-- A safety control must not be self-reported, so the server needs the same input
-- the agent had — the list of files the pull request actually touches.
--
-- The `pull_request` webhook payload carries diff STATS (additions, deletions,
-- changed_files) but never the file list, so this column is filled from GitHub's
-- PR Files API at PR open / synchronize / reopen and stays empty whenever that
-- fetch is unavailable (no App credentials, an API failure, a non-GitHub
-- provider row from migration 124).
--
-- EMPTY MEANS UNKNOWN, NEVER "NOTHING CHANGED". The resolver must fall back to
-- the risk map's fail-closed default on an empty array rather than reading zero
-- matched paths as "safe" — that inversion is the qa-evidence-floor bug with
-- merge semantics attached.
ALTER TABLE github_pull_request
    ADD COLUMN IF NOT EXISTS changed_paths TEXT[] NOT NULL DEFAULT '{}';
