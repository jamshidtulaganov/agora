-- Preserve shared workspace history while allowing a user account to be
-- permanently deleted. Personal/auth records already cascade; authorship and
-- ownership references become NULL. Invitations created by the deleted user
-- are removed because they are active authority-bearing records, not history.

ALTER TABLE agent_runtime
    DROP CONSTRAINT agent_runtime_owner_id_fkey,
    ADD CONSTRAINT agent_runtime_owner_id_fkey
        FOREIGN KEY (owner_id) REFERENCES "user"(id) ON DELETE SET NULL;

ALTER TABLE agent
    DROP CONSTRAINT agent_owner_id_fkey,
    ADD CONSTRAINT agent_owner_id_fkey
        FOREIGN KEY (owner_id) REFERENCES "user"(id) ON DELETE SET NULL,
    DROP CONSTRAINT agent_archived_by_fkey,
    ADD CONSTRAINT agent_archived_by_fkey
        FOREIGN KEY (archived_by) REFERENCES "user"(id) ON DELETE SET NULL;

ALTER TABLE skill
    DROP CONSTRAINT skill_created_by_fkey,
    ADD CONSTRAINT skill_created_by_fkey
        FOREIGN KEY (created_by) REFERENCES "user"(id) ON DELETE SET NULL;

ALTER TABLE lark_installation
    ALTER COLUMN installer_user_id DROP NOT NULL,
    DROP CONSTRAINT lark_installation_installer_user_id_fkey,
    ADD CONSTRAINT lark_installation_installer_user_id_fkey
        FOREIGN KEY (installer_user_id) REFERENCES "user"(id) ON DELETE SET NULL;

ALTER TABLE workspace_invitation
    DROP CONSTRAINT workspace_invitation_inviter_id_fkey,
    ADD CONSTRAINT workspace_invitation_inviter_id_fkey
        FOREIGN KEY (inviter_id) REFERENCES "user"(id) ON DELETE CASCADE,
    DROP CONSTRAINT workspace_invitation_invitee_user_id_fkey,
    ADD CONSTRAINT workspace_invitation_invitee_user_id_fkey
        FOREIGN KEY (invitee_user_id) REFERENCES "user"(id) ON DELETE SET NULL;
