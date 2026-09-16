-- name: ListActivitiesForIssue :many
-- All activities for an issue in chronological order, capped at $2 (DB safety
-- net to bound the response).
SELECT * FROM activity_log
WHERE issue_id = $1
ORDER BY created_at ASC, id ASC
LIMIT $2;

-- name: GetActivity :one
SELECT * FROM activity_log
WHERE id = $1;

-- name: CreateActivity :one
INSERT INTO activity_log (
    workspace_id, issue_id, actor_type, actor_id, action, details
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: HasSquadLeaderNoActionEvaluationForTask :one
SELECT EXISTS (
  SELECT 1
  FROM activity_log
  WHERE issue_id = @issue_id
    AND actor_type = 'agent'
    AND actor_id = @agent_id
    AND action = 'squad_leader_evaluated'
    AND details->>'outcome' = 'no_action'
    AND details->>'task_id' = @task_id::text
) AS exists;

-- name: CountAssigneeChangesByActor :many
-- Count how many times a user assigned each target via assignee_changed activities.
SELECT
  details->>'to_type' as assignee_type,
  details->>'to_id' as assignee_id,
  COUNT(*)::bigint as frequency
FROM activity_log
WHERE workspace_id = $1
  AND actor_id = $2
  AND actor_type = 'member'
  AND action = 'assignee_changed'
  AND details->>'to_type' IS NOT NULL
  AND details->>'to_id' IS NOT NULL
GROUP BY details->>'to_type', details->>'to_id';

-- name: ListRecentActivities :many
-- Recent workspace activity, newest first, for the assistant's digest tool.
-- The LEFT JOIN carries the owning issue's number + title so a caller can
-- render "MUL-12 — Fix login" without an N+1; workspace-level activities
-- (no issue) keep a NULL issue and are still returned.
--
-- NOT visibility-gated in SQL on purpose: the caller filters rows through
-- Queries.IssueBelongsToUser, so the non-owner gate stays defined in exactly
-- one place instead of gaining a fourth hand-copied ownership predicate.
SELECT a.*, i.number AS issue_number, i.title AS issue_title
FROM activity_log a
LEFT JOIN issue i ON i.id = a.issue_id
WHERE a.workspace_id = $1 AND a.created_at >= sqlc.arg('since')
ORDER BY a.created_at DESC, a.id DESC
LIMIT sqlc.arg('limit');
