-- name: GetActiveWorkspaceDesignContext :one
SELECT * FROM design_context_revision
WHERE workspace_id = $1 AND project_id IS NULL AND status = 'active'
LIMIT 1;

-- name: GetProposedWorkspaceDesignContext :one
SELECT * FROM design_context_revision
WHERE workspace_id = $1 AND project_id IS NULL AND status = 'proposed'
LIMIT 1;

-- name: GetActiveProjectDesignContext :one
SELECT * FROM design_context_revision
WHERE workspace_id = $1 AND project_id = $2 AND status = 'active'
LIMIT 1;

-- name: GetProposedProjectDesignContext :one
SELECT * FROM design_context_revision
WHERE workspace_id = $1 AND project_id = $2 AND status = 'proposed'
LIMIT 1;

-- name: GetNextWorkspaceDesignContextRevision :one
SELECT (COALESCE(MAX(revision), 0) + 1)::int
FROM design_context_revision
WHERE workspace_id = $1 AND project_id IS NULL;

-- name: GetNextProjectDesignContextRevision :one
SELECT (COALESCE(MAX(revision), 0) + 1)::int
FROM design_context_revision
WHERE workspace_id = $1 AND project_id = $2;

-- name: ListWorkspaceDesignContextHistory :many
SELECT * FROM design_context_revision
WHERE workspace_id = $1 AND project_id IS NULL
ORDER BY revision DESC
LIMIT $2;

-- name: ListProjectDesignContextHistory :many
SELECT * FROM design_context_revision
WHERE workspace_id = $1 AND project_id = $2
ORDER BY revision DESC
LIMIT $3;

-- name: CreateWorkspaceDesignContextProposal :one
INSERT INTO design_context_revision (
    workspace_id, project_id, revision, base_revision, status, context,
    context_hash, source_hash, sources, proposed_by_type, proposed_by_id, generated_at
) VALUES (
    sqlc.arg('workspace_id'), NULL, sqlc.arg('revision'), sqlc.arg('base_revision'), 'proposed', sqlc.arg('context'),
    sqlc.arg('context_hash'), sqlc.arg('source_hash'), sqlc.arg('sources'), sqlc.arg('proposed_by_type'),
    sqlc.narg('proposed_by_id'), sqlc.narg('generated_at')
)
RETURNING *;

-- name: CreateProjectDesignContextProposal :one
INSERT INTO design_context_revision (
    workspace_id, project_id, revision, base_revision, status, context,
    context_hash, source_hash, sources, proposed_by_type, proposed_by_id, generated_at
) VALUES (
    sqlc.arg('workspace_id'), sqlc.arg('project_id'), sqlc.arg('revision'), sqlc.arg('base_revision'), 'proposed', sqlc.arg('context'),
    sqlc.arg('context_hash'), sqlc.arg('source_hash'), sqlc.arg('sources'), sqlc.arg('proposed_by_type'),
    sqlc.narg('proposed_by_id'), sqlc.narg('generated_at')
)
RETURNING *;

-- name: SupersedeDesignContextRevision :execrows
UPDATE design_context_revision
SET status = 'superseded', reviewed_at = now(), reviewed_by = sqlc.arg('reviewed_by')
WHERE id = sqlc.arg('id') AND status = 'active' AND revision = sqlc.arg('revision');

-- name: ActivateDesignContextProposal :one
UPDATE design_context_revision
SET status = 'active', reviewed_at = now(), reviewed_by = sqlc.arg('reviewed_by')
WHERE id = sqlc.arg('id') AND status = 'proposed' AND base_revision = sqlc.arg('base_revision')
RETURNING *;

-- name: RejectDesignContextProposal :one
UPDATE design_context_revision
SET status = 'rejected', reviewed_at = now(), reviewed_by = sqlc.arg('reviewed_by'), rejection_reason = sqlc.arg('rejection_reason')
WHERE id = sqlc.arg('id') AND status = 'proposed' AND base_revision = sqlc.arg('base_revision')
RETURNING *;
