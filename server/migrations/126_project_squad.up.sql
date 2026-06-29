-- Bind a project to a single squad. When set, only that squad and its member
-- agents (or the squad leader) may be assigned to the project's issues — the
-- enforcement lives in the issue-assignment handlers, this column is the
-- source of truth. Nullable: a project with no squad keeps the prior open
-- behaviour (any agent/squad may work it). ON DELETE SET NULL so archiving or
-- hard-deleting a squad never orphans a project row.
ALTER TABLE project
    ADD COLUMN squad_id uuid REFERENCES squad(id) ON DELETE SET NULL;

-- Look up "which projects belong to this squad" cheaply (squad detail page,
-- future reporting). Partial index — most projects have no squad binding.
CREATE INDEX idx_project_squad_id ON project(squad_id) WHERE squad_id IS NOT NULL;
