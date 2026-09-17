-- A confirmed delete_workspace must not erase its own authorization and
-- outcome record. The target snapshot remains in target/summary.
ALTER TABLE assistant_pending_operation
    DROP CONSTRAINT assistant_pending_operation_workspace_id_fkey,
    ADD CONSTRAINT assistant_pending_operation_workspace_id_fkey
        FOREIGN KEY (workspace_id) REFERENCES workspace(id) ON DELETE SET NULL;
