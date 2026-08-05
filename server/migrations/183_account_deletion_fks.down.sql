ALTER TABLE workspace_invitation
    DROP CONSTRAINT workspace_invitation_inviter_id_fkey,
    ADD CONSTRAINT workspace_invitation_inviter_id_fkey
        FOREIGN KEY (inviter_id) REFERENCES "user"(id),
    DROP CONSTRAINT workspace_invitation_invitee_user_id_fkey,
    ADD CONSTRAINT workspace_invitation_invitee_user_id_fkey
        FOREIGN KEY (invitee_user_id) REFERENCES "user"(id);

-- A rollback cannot restore the historical installer identity once its user
-- was deleted. Remove only those now-unowned installations before restoring
-- the old NOT NULL + RESTRICT invariant.
DELETE FROM lark_installation WHERE installer_user_id IS NULL;

ALTER TABLE lark_installation
    DROP CONSTRAINT lark_installation_installer_user_id_fkey,
    ALTER COLUMN installer_user_id SET NOT NULL,
    ADD CONSTRAINT lark_installation_installer_user_id_fkey
        FOREIGN KEY (installer_user_id) REFERENCES "user"(id) ON DELETE RESTRICT;

ALTER TABLE skill
    DROP CONSTRAINT skill_created_by_fkey,
    ADD CONSTRAINT skill_created_by_fkey
        FOREIGN KEY (created_by) REFERENCES "user"(id);

ALTER TABLE agent
    DROP CONSTRAINT agent_owner_id_fkey,
    ADD CONSTRAINT agent_owner_id_fkey
        FOREIGN KEY (owner_id) REFERENCES "user"(id),
    DROP CONSTRAINT agent_archived_by_fkey,
    ADD CONSTRAINT agent_archived_by_fkey
        FOREIGN KEY (archived_by) REFERENCES "user"(id);

ALTER TABLE agent_runtime
    DROP CONSTRAINT agent_runtime_owner_id_fkey,
    ADD CONSTRAINT agent_runtime_owner_id_fkey
        FOREIGN KEY (owner_id) REFERENCES "user"(id);
