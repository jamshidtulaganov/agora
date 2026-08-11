CREATE TABLE design_context_revision (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id UUID REFERENCES project(id) ON DELETE CASCADE,
    revision INT NOT NULL CHECK (revision >= 1),
    base_revision INT NOT NULL DEFAULT 0 CHECK (base_revision >= 0),
    status TEXT NOT NULL CHECK (status IN ('proposed', 'active', 'rejected', 'superseded')),
    context JSONB NOT NULL CHECK (jsonb_typeof(context) = 'object'),
    context_hash TEXT NOT NULL,
    source_hash TEXT NOT NULL,
    sources JSONB NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(sources) = 'array'),
    proposed_by_type TEXT NOT NULL CHECK (proposed_by_type IN ('member', 'agent', 'system')),
    proposed_by_id UUID,
    reviewed_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    generated_at TIMESTAMPTZ,
    proposed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at TIMESTAMPTZ,
    rejection_reason TEXT NOT NULL DEFAULT '',
    CHECK (base_revision < revision)
);

CREATE UNIQUE INDEX design_context_revision_scope_revision_uq
    ON design_context_revision(workspace_id, COALESCE(project_id, '00000000-0000-0000-0000-000000000000'::uuid), revision);
CREATE UNIQUE INDEX design_context_revision_active_uq
    ON design_context_revision(workspace_id, COALESCE(project_id, '00000000-0000-0000-0000-000000000000'::uuid))
    WHERE status = 'active';
CREATE UNIQUE INDEX design_context_revision_proposed_uq
    ON design_context_revision(workspace_id, COALESCE(project_id, '00000000-0000-0000-0000-000000000000'::uuid))
    WHERE status = 'proposed';
CREATE INDEX design_context_revision_history_idx
    ON design_context_revision(workspace_id, project_id, revision DESC);

-- Preserve existing deployments as unverified active revision rows, then
-- remove the large blobs from generic settings. New proposals must carry the
-- strict v1 schema and authoritative source hashes enforced by the server.
INSERT INTO design_context_revision (
    workspace_id, project_id, revision, base_revision, status, context,
    context_hash, source_hash, sources, proposed_by_type, reviewed_at
)
SELECT id, NULL,
       CASE
           WHEN COALESCE(settings->'design_manifest'->>'revision', '') ~ '^[1-9][0-9]{0,8}$'
               THEN (settings->'design_manifest'->>'revision')::int
           ELSE 1
       END,
       0, 'active',
       (settings->'design_manifest') - 'source' - 'revision' - 'updated_at',
       md5((settings->'design_manifest')::text), '', '[]', 'system', now()
FROM workspace
WHERE jsonb_typeof(settings->'design_manifest') = 'object';

INSERT INTO design_context_revision (
    workspace_id, project_id, revision, base_revision, status, context,
    context_hash, source_hash, sources, proposed_by_type, reviewed_at
)
SELECT workspace_id, id,
       CASE
           WHEN COALESCE(settings->'design_manifest'->>'revision', '') ~ '^[1-9][0-9]{0,8}$'
               THEN (settings->'design_manifest'->>'revision')::int
           ELSE 1
       END,
       0, 'active',
       (settings->'design_manifest') - 'source' - 'revision' - 'updated_at',
       md5((settings->'design_manifest')::text), '', '[]', 'system', now()
FROM project
WHERE jsonb_typeof(settings->'design_manifest') = 'object';

UPDATE workspace SET settings = COALESCE(settings, '{}'::jsonb) - 'design_manifest'
WHERE settings ? 'design_manifest';
UPDATE project SET settings = COALESCE(settings, '{}'::jsonb) - 'design_manifest'
WHERE settings ? 'design_manifest';
