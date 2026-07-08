-- Archive individual issues (distinct from the terminal `done` status). Used to
-- retire Bitrix-imported tasks that arrive already-done on the dev-team kanban
-- stage so they don't populate the active board. Nullable — a live issue has
-- archived_at IS NULL. Partial index keeps the board query (archived_at IS NULL)
-- fast on large workspaces.
ALTER TABLE issue ADD COLUMN IF NOT EXISTS archived_at timestamptz;
CREATE INDEX IF NOT EXISTS idx_issue_active ON issue (workspace_id) WHERE archived_at IS NULL;
