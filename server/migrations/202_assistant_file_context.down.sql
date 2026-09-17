ALTER TABLE assistant_run
    DROP COLUMN context_snapshot,
    DROP COLUMN context_attachment_ids,
    DROP COLUMN context_project_id;
