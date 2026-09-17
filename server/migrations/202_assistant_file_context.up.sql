-- Capture the selected project and files at message acceptance. UUIDs are
-- snapshots rather than cascading references: a later delete must not change
-- the meaning of an already accepted user message.
ALTER TABLE assistant_run
    ADD COLUMN context_project_id UUID,
    ADD COLUMN context_attachment_ids UUID[] NOT NULL DEFAULT '{}',
    ADD COLUMN context_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb;
