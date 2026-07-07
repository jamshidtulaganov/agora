-- Down: collapse back to one token per (user, provider). Workspace-scoped
-- rows are dropped (the global rows are the pre-148 shape).
DELETE FROM user_editor_token WHERE workspace_id IS NOT NULL;
DROP INDEX IF EXISTS user_editor_token_global_idx;
DROP INDEX IF EXISTS user_editor_token_workspace_idx;
ALTER TABLE user_editor_token DROP COLUMN workspace_id;
ALTER TABLE user_editor_token ADD PRIMARY KEY (user_id, provider);
