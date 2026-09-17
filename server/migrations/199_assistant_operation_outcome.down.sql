ALTER TABLE assistant_pending_operation
    DROP COLUMN IF EXISTS outcome,
    DROP COLUMN IF EXISTS executing_at;
