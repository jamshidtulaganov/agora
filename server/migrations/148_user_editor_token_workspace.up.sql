-- Per-WORKSPACE editor token overrides. workspace_id NULL = the user's global
-- default; a row with a workspace_id overrides the global one for editors
-- opened on that workspace's issues (work vs personal GitHub identity per
-- workspace). Resolution lives in handler.editorEnvForUser: workspace-specific
-- wins per provider, else global.
ALTER TABLE user_editor_token
    ADD COLUMN workspace_id uuid REFERENCES workspace (id) ON DELETE CASCADE;

-- The (user_id, provider) PK can't express "one global + one per workspace";
-- replace it with NULL-aware partial unique indexes.
ALTER TABLE user_editor_token DROP CONSTRAINT user_editor_token_pkey;

CREATE UNIQUE INDEX user_editor_token_global_idx
    ON user_editor_token (user_id, provider) WHERE workspace_id IS NULL;
CREATE UNIQUE INDEX user_editor_token_workspace_idx
    ON user_editor_token (user_id, provider, workspace_id) WHERE workspace_id IS NOT NULL;
