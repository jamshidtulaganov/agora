UPDATE workspace w
SET settings = jsonb_set(
    COALESCE(w.settings, '{}'::jsonb),
    '{design_manifest}',
    r.context || jsonb_build_object('revision', r.revision, 'source', 'mixed', 'updated_at', COALESCE(r.reviewed_at, r.proposed_at)),
    true
)
FROM design_context_revision r
WHERE r.workspace_id = w.id AND r.project_id IS NULL AND r.status = 'active';

UPDATE project p
SET settings = jsonb_set(
    COALESCE(p.settings, '{}'::jsonb),
    '{design_manifest}',
    r.context || jsonb_build_object('revision', r.revision, 'source', 'mixed', 'updated_at', COALESCE(r.reviewed_at, r.proposed_at)),
    true
)
FROM design_context_revision r
WHERE r.project_id = p.id AND r.workspace_id = p.workspace_id AND r.status = 'active';

DROP TABLE design_context_revision;
