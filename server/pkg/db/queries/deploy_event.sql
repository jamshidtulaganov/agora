-- Deploy events — the durable, immutable-append record of Tier-1 (QA-box
-- git-sync) deploys. Gives the SDLC stepper's Deploy stage a real
-- deploySynced signal instead of the lossy connected_box.last_branch proxy.
-- No upsert: every sync attempt (success or failure) writes a NEW row, same
-- append-only discipline as qa_evidence.

-- name: InsertDeployEvent :one
-- Record one deploy attempt (success or failed) for an issue.
INSERT INTO deploy_event (
    workspace_id,
    issue_id,
    ref,
    target,
    status,
    summary,
    captured_at
)
VALUES ($1, $2, $3, $4, $5, $6, now())
RETURNING *;

-- name: GetLatestDeployEventForIssue :one
-- The freshest deploy event for an issue (what deploySynced derives from).
SELECT * FROM deploy_event
WHERE issue_id = $1 AND workspace_id = $2
ORDER BY captured_at DESC
LIMIT 1;

-- name: ListDeployEventsForIssue :many
-- A few recent deploy events for an issue (the Deploy lens's history rows).
SELECT * FROM deploy_event
WHERE issue_id = $1 AND workspace_id = $2
ORDER BY captured_at DESC
LIMIT $3;
