ALTER TABLE assistant_pending_operation
    DROP CONSTRAINT assistant_pending_operation_workspace_id_fkey,
    ADD CONSTRAINT assistant_pending_operation_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspace(id) ON DELETE CASCADE;
