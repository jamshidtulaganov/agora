-- Per-project settings blob. Mirrors workspace.settings (001_init): a
-- NOT NULL jsonb defaulting to '{}' so every existing row backfills to an
-- empty object and the handler can read/merge keys without a null guard.
--
-- First consumer is the per-project "sprint mode" toggle
-- (settings.sprint_mode), previously a client-only localStorage flag
-- (agora_sprint_mode:{projectId}). Future per-project preferences live here
-- too rather than as one-off columns.
ALTER TABLE project ADD COLUMN settings jsonb NOT NULL DEFAULT '{}';
