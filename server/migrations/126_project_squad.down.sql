DROP INDEX IF EXISTS idx_project_squad_id;
ALTER TABLE project DROP COLUMN IF EXISTS squad_id;
