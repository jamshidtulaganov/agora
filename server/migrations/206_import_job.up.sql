-- One import run, as a row. Deliberately NOT a package-level struct: the
-- Bitrix importer's bitrixImportProgressState is a single process-wide
-- variable with a mutex and a Cancel func, which means one import at a time
-- per process, progress lost on restart, and invisible to a second instance.
-- A row gives: progress that survives a restart, two workspaces importing
-- concurrently, a second run in the same workspace refused with a pointer at
-- the one already going (rather than cancelling it), a receipt that is
-- reconstructible after the fact, and per-tenant queryability like everything
-- else.
CREATE TABLE import_job (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    -- SET NULL, not CASCADE: deleting the credential must not delete the
    -- receipt of what was already imported with it.
    connection_id uuid REFERENCES import_connection(id) ON DELETE SET NULL,
    source        text NOT NULL,                       -- linear | jira | …
    -- pending | dry_run | awaiting_confirm | running | done | failed | cancelled
    -- No CHECK: a new posture must not need a migration, and an unknown value
    -- renders as a generic state rather than failing a write.
    status        text NOT NULL DEFAULT 'pending',
    scope         jsonb NOT NULL DEFAULT '{}'::jsonb,  -- which containers, date window, options
    plan          jsonb,                               -- the dry-run result the human confirmed
    mapping       jsonb,                               -- the mapping actually used, frozen at confirm
    totals        jsonb NOT NULL DEFAULT '{}'::jsonb,  -- created/updated/skipped/failed per entity kind
    failures      jsonb NOT NULL DEFAULT '[]'::jsonb,  -- bounded list: {kind, identifier, reason}
    artifact_id   uuid,                                -- the receipt artifact, when one was written
    created_by    uuid REFERENCES "user"(id) ON DELETE SET NULL,
    started_at    timestamptz,
    finished_at   timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT import_job_scope_is_object    CHECK (jsonb_typeof(scope) = 'object'),
    CONSTRAINT import_job_totals_is_object   CHECK (jsonb_typeof(totals) = 'object'),
    CONSTRAINT import_job_failures_is_array  CHECK (jsonb_typeof(failures) = 'array')
);

-- The workspace's job history, newest first.
CREATE INDEX idx_import_job_workspace_created
    ON import_job (workspace_id, created_at DESC);

-- "Is an import already going in this workspace?" — the check that replaces the
-- Bitrix global. Partial, so it stays tiny however long the history grows.
CREATE INDEX idx_import_job_active
    ON import_job (workspace_id)
    WHERE status IN ('pending', 'dry_run', 'awaiting_confirm', 'running');
