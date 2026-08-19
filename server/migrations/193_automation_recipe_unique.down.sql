-- The dedup DELETE in the up migration is not reversible (the doubled rows are
-- gone); dropping the index restores the pre-193 schema.
DROP INDEX IF EXISTS idx_automation_recipe_flow_once;
