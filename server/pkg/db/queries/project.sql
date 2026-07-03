-- name: ListProjects :many
SELECT * FROM project
WHERE workspace_id = $1
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
  AND (sqlc.narg('priority')::text IS NULL OR priority = sqlc.narg('priority'))
ORDER BY created_at DESC;

-- name: GetProject :one
SELECT * FROM project
WHERE id = $1;

-- name: GetProjectInWorkspace :one
SELECT * FROM project
WHERE id = $1 AND workspace_id = $2;

-- name: CreateProject :one
INSERT INTO project (
    workspace_id, title, description, icon, status,
    lead_type, lead_id, priority, squad_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, sqlc.narg('squad_id')
) RETURNING *;

-- name: UpdateProject :one
UPDATE project SET
    title = COALESCE(sqlc.narg('title'), title),
    description = sqlc.narg('description'),
    icon = sqlc.narg('icon'),
    status = COALESCE(sqlc.narg('status'), status),
    priority = COALESCE(sqlc.narg('priority'), priority),
    lead_type = sqlc.narg('lead_type'),
    lead_id = sqlc.narg('lead_id'),
    squad_id = sqlc.narg('squad_id'),
    settings = COALESCE(sqlc.narg('settings'), settings),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteProject :exec
-- Defense-in-depth: workspace_id is a SQL-layer tenant guard. See DeleteIssue.
DELETE FROM project WHERE id = $1 AND workspace_id = $2;

-- name: SetProjectDesignManifest :one
-- KEY-SCOPED write of project.settings.design_manifest. Unlike UpdateProject
-- (which replaces the whole settings blob from a client-side snapshot and so
-- races concurrent writes to sibling keys like qa_manifest), this touches ONLY
-- the design_manifest key via jsonb_set, so an agent capture and a human edit
-- can never clobber each other's other settings. Workspace-guarded.
UPDATE project SET
    settings = jsonb_set(COALESCE(settings, '{}'::jsonb), '{design_manifest}', sqlc.arg('manifest')::jsonb, true),
    updated_at = now()
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id')
RETURNING *;

-- name: SetProjectSettingKey :one
-- KEY-SCOPED write of a single scalar settings key (design_agent, design_auto).
-- Same jsonb_set rationale as SetProjectDesignManifest — the key is passed as a
-- text path element so one endpoint can set any of the scalar design keys
-- without a read-modify-write of the whole blob.
UPDATE project SET
    settings = jsonb_set(COALESCE(settings, '{}'::jsonb), ARRAY[sqlc.arg('key')::text], sqlc.arg('value')::jsonb, true),
    updated_at = now()
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id')
RETURNING *;

-- name: CountIssuesByProject :one
SELECT count(*) FROM issue
WHERE project_id = $1;

-- name: GetProjectIssueStats :many
SELECT project_id,
       count(*)::bigint AS total_count,
       count(*) FILTER (WHERE status IN ('done', 'cancelled'))::bigint AS done_count
FROM issue
WHERE project_id = ANY(sqlc.arg('project_ids')::uuid[])
GROUP BY project_id;

-- name: ListRiskMappedProjects :many
-- Projects that opted into the legacy safety spine (settings.risk_map set).
-- The config watchdog sweeps these to verify their knowledge/QA artifacts
-- (KB skill, qa_manifest, base suite) actually exist — a silently-missing
-- artifact would otherwise read as "covered".
SELECT * FROM project
WHERE settings ? 'risk_map'
  AND status NOT IN ('completed', 'cancelled')
ORDER BY created_at;
