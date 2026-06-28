-- Explicitly bind a Remote Box to a project. A box's repo (a developer's fork,
-- e.g. jamshidtulaganov/sd-main) legitimately differs from the project's bound
-- repo (the upstream, e.g. azizkh/sd) — and a renamed fork breaks repo-name
-- matching entirely. So the box→project link is explicit, not derived: an issue's
-- QA box is the connected_box bound to the issue's project. Nullable + ON DELETE
-- SET NULL so an unbound box (or a deleted project) never dangles.
ALTER TABLE connected_box
    ADD COLUMN project_id uuid REFERENCES project(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_connected_box_project ON connected_box (project_id);
