-- Recipe installs must be once-per-workspace at the DATABASE level. The handler
-- already refuses a duplicate install (check-then-insert), but two concurrent
-- installs race past that check — the exact incident that once stacked eleven
-- flows from three installs. A partial unique index closes the window.
--
-- Dedup first: deployments that hit the incident may still carry doubled rows,
-- which would fail the index build. Keep the OLDEST row per (workspace, recipe,
-- flow name) — the one the team has been editing — and drop the copies. Their
-- automation_run rows cascade with them (the survivors keep their own history).
DELETE FROM automation a
USING automation keeper
WHERE a.recipe_key <> ''
  AND keeper.workspace_id = a.workspace_id
  AND keeper.recipe_key = a.recipe_key
  AND keeper.name = a.name
  AND (keeper.created_at < a.created_at
       OR (keeper.created_at = a.created_at AND keeper.id < a.id));

CREATE UNIQUE INDEX idx_automation_recipe_flow_once
    ON automation (workspace_id, recipe_key, name)
    WHERE recipe_key <> '';
