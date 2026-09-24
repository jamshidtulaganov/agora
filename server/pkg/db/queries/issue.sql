-- name: ListIssues :many
-- involves_user_id widens the assignee filter to surface issues where the user
-- is *indirectly* the assignee — via an owned agent or a squad they belong to /
-- lead / have an agent inside. The semantics intentionally exclude direct
-- member assignment (`assignee_type='member' AND assignee_id=involves_user_id`)
-- because that is already the meaning of the `assignee_id` filter (tab 1
-- "Assigned to me"), and the two filters must produce disjoint result sets.
SELECT i.id, i.workspace_id, i.title, i.description, i.status, i.priority,
       i.assignee_type, i.assignee_id, i.creator_type, i.creator_id,
       i.parent_issue_id, i.position, i.start_date, i.due_date, i.created_at, i.updated_at, i.number, i.project_id, i.metadata, i.archived_at
FROM issue i
WHERE i.workspace_id = $1
  -- Archived issues (retired done-from-Bitrix tasks) stay off the board unless
  -- the caller explicitly opts into an archived view.
  AND (sqlc.narg('include_archived')::bool IS TRUE OR i.archived_at IS NULL)
  AND (sqlc.narg('status')::text IS NULL OR i.status = sqlc.narg('status'))
  AND (sqlc.narg('priority')::text IS NULL OR i.priority = sqlc.narg('priority'))
  AND (sqlc.narg('assignee_id')::uuid IS NULL OR i.assignee_id = sqlc.narg('assignee_id'))
  AND (sqlc.narg('assignee_ids')::uuid[] IS NULL OR i.assignee_id = ANY(sqlc.narg('assignee_ids')::uuid[]))
  AND (sqlc.narg('creator_id')::uuid IS NULL OR i.creator_id = sqlc.narg('creator_id'))
  AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('scheduled')::bool IS NULL OR (i.start_date IS NOT NULL OR i.due_date IS NOT NULL))
  AND (sqlc.narg('metadata_filter')::jsonb IS NULL OR i.metadata @> sqlc.narg('metadata_filter')::jsonb)
  AND (
    sqlc.narg('involves_user_id')::uuid IS NULL
    -- (1) assignee is an agent owned by the user
    OR (i.assignee_type = 'agent' AND i.assignee_id IN (
          SELECT a.id FROM agent a
           WHERE a.workspace_id = $1
             AND a.owner_id     = sqlc.narg('involves_user_id')::uuid
    ))
    -- (2)(3)(4) assignee is a squad related to the user — three relations
    OR (i.assignee_type = 'squad' AND i.assignee_id IN (
          -- (2) the user is a human member of the squad
          SELECT sm.squad_id
            FROM squad_member sm
            JOIN squad s ON s.id = sm.squad_id
           WHERE s.workspace_id = $1
             AND sm.member_type = 'member'
             AND sm.member_id   = sqlc.narg('involves_user_id')::uuid
          UNION
          -- (3) the squad's canonical leader is an agent owned by the user.
          -- We read squad.leader_id directly rather than relying on a
          -- squad_member row, because the leader copy in squad_member is
          -- best-effort (see squad.go AddSquadMember error handling).
          SELECT s.id
            FROM squad s
            JOIN agent a ON a.id = s.leader_id
           WHERE s.workspace_id = $1
             AND a.workspace_id = $1
             AND a.owner_id     = sqlc.narg('involves_user_id')::uuid
          UNION
          -- (4) the squad has an agent member owned by the user
          SELECT sm.squad_id
            FROM squad_member sm
            JOIN squad s ON s.id = sm.squad_id
            JOIN agent a ON a.id = sm.member_id
           WHERE s.workspace_id = $1
             AND sm.member_type = 'agent'
             AND a.workspace_id = $1
             AND a.owner_id     = sqlc.narg('involves_user_id')::uuid
    ))
  )
  -- Restricted visibility: a non-owner member sees ONLY issues that are theirs
  -- — created by them, assigned to them directly, to an agent they own, or to
  -- a squad they (or an agent they own) belong to / lead. NULL disables the gate (owners see
  -- everything). This is the AND-gate counterpart of involves_user_id, and it
  -- additionally covers DIRECT member assignment.
  AND (
    sqlc.narg('restrict_to_user')::uuid IS NULL
    OR (i.creator_type = 'member' AND i.creator_id = sqlc.narg('restrict_to_user')::uuid)
    OR (i.assignee_type = 'member' AND i.assignee_id = sqlc.narg('restrict_to_user')::uuid)
    OR (i.assignee_type = 'agent' AND i.assignee_id IN (
          SELECT a.id FROM agent a
           WHERE a.workspace_id = $1 AND a.owner_id = sqlc.narg('restrict_to_user')::uuid))
    OR (i.assignee_type = 'squad' AND i.assignee_id IN (
          SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id
           WHERE s.workspace_id = $1 AND sm.member_type = 'member'
             AND sm.member_id = sqlc.narg('restrict_to_user')::uuid
          UNION
          SELECT s.id FROM squad s JOIN agent a ON a.id = s.leader_id
           WHERE s.workspace_id = $1 AND a.owner_id = sqlc.narg('restrict_to_user')::uuid
          UNION
          SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id
            JOIN agent a ON a.id = sm.member_id
           WHERE s.workspace_id = $1 AND sm.member_type = 'agent'
             AND a.owner_id = sqlc.narg('restrict_to_user')::uuid))
  )
ORDER BY i.position ASC, i.created_at DESC
LIMIT $2 OFFSET $3;

-- name: GetIssue :one
SELECT * FROM issue
WHERE id = $1;

-- name: GetIssueInWorkspace :one
SELECT * FROM issue
WHERE id = $1 AND workspace_id = $2;

-- name: IssueBelongsToUser :one
-- Whether the issue is owned by the user: created by them, assigned to them
-- directly (member), to an agent they own, or to a squad they (or an agent they
-- own) belong to / lead. Gates issue detail for non-owner members (mirrors
-- issueOwnershipClause).
SELECT EXISTS (
  SELECT 1 FROM issue i
  WHERE i.id = @issue_id AND i.workspace_id = @workspace_id
    AND (
      (i.creator_type = 'member' AND i.creator_id = @user_id)
      OR (i.assignee_type = 'member' AND i.assignee_id = @user_id)
      OR (i.assignee_type = 'agent' AND i.assignee_id IN (
            SELECT a.id FROM agent a WHERE a.workspace_id = @workspace_id AND a.owner_id = @user_id))
      OR (i.assignee_type = 'squad' AND i.assignee_id IN (
            SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id
             WHERE s.workspace_id = @workspace_id AND sm.member_type = 'member' AND sm.member_id = @user_id
            UNION SELECT s.id FROM squad s JOIN agent a ON a.id = s.leader_id
             WHERE s.workspace_id = @workspace_id AND a.owner_id = @user_id
            UNION SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id
              JOIN agent a ON a.id = sm.member_id
             WHERE s.workspace_id = @workspace_id AND sm.member_type = 'agent' AND a.owner_id = @user_id))
    )
);

-- name: CreateIssue :one
INSERT INTO issue (
    workspace_id, title, description, status, priority,
    assignee_type, assignee_id, creator_type, creator_id,
    parent_issue_id, position, start_date, due_date, number, project_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
) RETURNING *;

-- name: GetIssueByNumber :one
SELECT * FROM issue
WHERE workspace_id = $1 AND number = $2;

-- name: UpdateIssue :one
UPDATE issue SET
    title = COALESCE(sqlc.narg('title'), title),
    description = COALESCE(sqlc.narg('description'), description),
    status = COALESCE(sqlc.narg('status'), status),
    priority = COALESCE(sqlc.narg('priority'), priority),
    assignee_type = sqlc.narg('assignee_type'),
    assignee_id = sqlc.narg('assignee_id'),
    position = COALESCE(sqlc.narg('position'), position),
    start_date = sqlc.narg('start_date'),
    due_date = sqlc.narg('due_date'),
    parent_issue_id = sqlc.narg('parent_issue_id'),
    project_id = sqlc.narg('project_id'),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateIssueStatus :one
-- Workspace_id in the WHERE clause is a SQL-layer tenant guard; see DeleteIssue.
UPDATE issue SET
    status = $2,
    updated_at = now()
WHERE id = $1 AND workspace_id = $3
RETURNING *;

-- name: SetIssueArchived :exec
-- Archive/unarchive an issue. Idempotent: only writes when the state actually
-- flips, so a re-sync of an already-archived done task is a no-op (no churn, no
-- updated_at bump / bus echo). Used by the Bitrix done-auto-archive path.
UPDATE issue SET archived_at = CASE WHEN @archived::bool THEN now() ELSE NULL END
WHERE id = @id
  AND ((@archived::bool AND archived_at IS NULL)
       OR ((NOT @archived::bool) AND archived_at IS NOT NULL));

-- name: PromoteIssueFromBacklog :one
-- Compare-and-swap promotion out of backlog: flips status to 'todo' ONLY when
-- the issue is still 'backlog'. Returns the row when it wins the swap; ErrNoRows
-- when another concurrent promoter already moved it (or it was never backlog).
-- Serializes the design-dependency promotion so two prerequisite siblings
-- finishing at once cannot both promote + double-enqueue the same dependent.
UPDATE issue SET
    status = 'todo',
    updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND status = 'backlog'
RETURNING *;

-- name: UpdateIssueAssignee :one
-- Focused assignee change (e.g. the Lark "assign to me" card action). Both
-- fields move together; pass NULL/NULL to unassign. Workspace_id guards tenancy.
UPDATE issue SET
    assignee_type = $2,
    assignee_id = $3,
    updated_at = now()
WHERE id = $1 AND workspace_id = $4
RETURNING *;

-- name: CreateIssueWithOrigin :one
INSERT INTO issue (
    workspace_id, title, description, status, priority,
    assignee_type, assignee_id, creator_type, creator_id,
    parent_issue_id, position, start_date, due_date, number, project_id,
    origin_type, origin_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
    sqlc.narg('origin_type'), sqlc.narg('origin_id')
) RETURNING *;

-- name: LockIssueDuplicateKey :exec
SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0));

-- name: FindActiveDuplicateIssue :one
SELECT * FROM issue
WHERE workspace_id = $1
  AND status NOT IN ('done', 'cancelled')
  AND project_id IS NOT DISTINCT FROM sqlc.arg('project_id')::uuid
  AND parent_issue_id IS NOT DISTINCT FROM sqlc.arg('parent_issue_id')::uuid
  AND lower(btrim(regexp_replace(title, '[[:space:]]+', ' ', 'g'))) = sqlc.arg('normalized_title')
ORDER BY created_at ASC
LIMIT 1;

-- name: DeleteIssue :exec
-- Defense-in-depth: the workspace_id predicate makes the tenant invariant a
-- SQL-layer guarantee rather than a handler-layer one. Handler loaders
-- (loadIssueForUser / GetIssueInWorkspace) already enforce membership today,
-- but a future loader bypass or a new caller skipping the loader would be
-- silently catastrophic without this guard. See incident #1661.
DELETE FROM issue WHERE id = $1 AND workspace_id = $2;

-- name: ListOpenIssues :many
-- See ListIssues for the semantics of involves_user_id (mirrors the 4-branch
-- filter; member-direct assignment is intentionally excluded).
SELECT i.id, i.workspace_id, i.title, i.description, i.status, i.priority,
       i.assignee_type, i.assignee_id, i.creator_type, i.creator_id,
       i.parent_issue_id, i.position, i.start_date, i.due_date, i.created_at, i.updated_at, i.number, i.project_id, i.metadata
FROM issue i
WHERE i.workspace_id = $1
  AND i.status NOT IN ('done', 'cancelled')
  AND (sqlc.narg('priority')::text IS NULL OR i.priority = sqlc.narg('priority'))
  AND (sqlc.narg('assignee_id')::uuid IS NULL OR i.assignee_id = sqlc.narg('assignee_id'))
  AND (sqlc.narg('assignee_ids')::uuid[] IS NULL OR i.assignee_id = ANY(sqlc.narg('assignee_ids')::uuid[]))
  AND (sqlc.narg('creator_id')::uuid IS NULL OR i.creator_id = sqlc.narg('creator_id'))
  AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('metadata_filter')::jsonb IS NULL OR i.metadata @> sqlc.narg('metadata_filter')::jsonb)
  AND (
    sqlc.narg('involves_user_id')::uuid IS NULL
    OR (i.assignee_type = 'agent' AND i.assignee_id IN (
          SELECT a.id FROM agent a
           WHERE a.workspace_id = $1
             AND a.owner_id     = sqlc.narg('involves_user_id')::uuid
    ))
    OR (i.assignee_type = 'squad' AND i.assignee_id IN (
          SELECT sm.squad_id
            FROM squad_member sm
            JOIN squad s ON s.id = sm.squad_id
           WHERE s.workspace_id = $1
             AND sm.member_type = 'member'
             AND sm.member_id   = sqlc.narg('involves_user_id')::uuid
          UNION
          SELECT s.id
            FROM squad s
            JOIN agent a ON a.id = s.leader_id
           WHERE s.workspace_id = $1
             AND a.workspace_id = $1
             AND a.owner_id     = sqlc.narg('involves_user_id')::uuid
          UNION
          SELECT sm.squad_id
            FROM squad_member sm
            JOIN squad s ON s.id = sm.squad_id
            JOIN agent a ON a.id = sm.member_id
           WHERE s.workspace_id = $1
             AND sm.member_type = 'agent'
             AND a.workspace_id = $1
             AND a.owner_id     = sqlc.narg('involves_user_id')::uuid
    ))
  )
  -- Restricted visibility (non-owner): only the caller's own issues. See ListIssues.
  AND (
    sqlc.narg('restrict_to_user')::uuid IS NULL
    OR (i.creator_type = 'member' AND i.creator_id = sqlc.narg('restrict_to_user')::uuid)
    OR (i.assignee_type = 'member' AND i.assignee_id = sqlc.narg('restrict_to_user')::uuid)
    OR (i.assignee_type = 'agent' AND i.assignee_id IN (
          SELECT a.id FROM agent a
           WHERE a.workspace_id = $1 AND a.owner_id = sqlc.narg('restrict_to_user')::uuid))
    OR (i.assignee_type = 'squad' AND i.assignee_id IN (
          SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id
           WHERE s.workspace_id = $1 AND sm.member_type = 'member'
             AND sm.member_id = sqlc.narg('restrict_to_user')::uuid
          UNION
          SELECT s.id FROM squad s JOIN agent a ON a.id = s.leader_id
           WHERE s.workspace_id = $1 AND a.owner_id = sqlc.narg('restrict_to_user')::uuid
          UNION
          SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id
            JOIN agent a ON a.id = sm.member_id
           WHERE s.workspace_id = $1 AND sm.member_type = 'agent'
             AND a.owner_id = sqlc.narg('restrict_to_user')::uuid))
  )
ORDER BY i.position ASC, i.created_at DESC;

-- name: CountIssues :one
-- The exact-total twin of ListIssues. Every predicate below is a copy of the
-- one there, in the same order, INCLUDING the archive filter and the non-owner
-- visibility gate — the two that used to be missing.
--
-- They have to match, because this is what the assistant's tool envelope
-- reports as scope.total next to a capped page of rows. A count taken under
-- looser predicates than the list it describes is worse than no count: the
-- model states it as fact, and the user reads "12 of 40" over a list whose real
-- total is 12.
--
-- See ListIssues for the semantics of involves_user_id and restrict_to_user.
SELECT count(*) FROM issue i
WHERE i.workspace_id = $1
  AND (sqlc.narg('include_archived')::bool IS TRUE OR i.archived_at IS NULL)
  AND (sqlc.narg('status')::text IS NULL OR i.status = sqlc.narg('status'))
  AND (sqlc.narg('priority')::text IS NULL OR i.priority = sqlc.narg('priority'))
  AND (sqlc.narg('assignee_id')::uuid IS NULL OR i.assignee_id = sqlc.narg('assignee_id'))
  AND (sqlc.narg('assignee_ids')::uuid[] IS NULL OR i.assignee_id = ANY(sqlc.narg('assignee_ids')::uuid[]))
  AND (sqlc.narg('creator_id')::uuid IS NULL OR i.creator_id = sqlc.narg('creator_id'))
  AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('scheduled')::bool IS NULL OR (i.start_date IS NOT NULL OR i.due_date IS NOT NULL))
  AND (sqlc.narg('metadata_filter')::jsonb IS NULL OR i.metadata @> sqlc.narg('metadata_filter')::jsonb)
  AND (
    sqlc.narg('involves_user_id')::uuid IS NULL
    OR (i.assignee_type = 'agent' AND i.assignee_id IN (
          SELECT a.id FROM agent a
           WHERE a.workspace_id = $1
             AND a.owner_id     = sqlc.narg('involves_user_id')::uuid
    ))
    OR (i.assignee_type = 'squad' AND i.assignee_id IN (
          SELECT sm.squad_id
            FROM squad_member sm
            JOIN squad s ON s.id = sm.squad_id
           WHERE s.workspace_id = $1
             AND sm.member_type = 'member'
             AND sm.member_id   = sqlc.narg('involves_user_id')::uuid
          UNION
          SELECT s.id
            FROM squad s
            JOIN agent a ON a.id = s.leader_id
           WHERE s.workspace_id = $1
             AND a.workspace_id = $1
             AND a.owner_id     = sqlc.narg('involves_user_id')::uuid
          UNION
          SELECT sm.squad_id
            FROM squad_member sm
            JOIN squad s ON s.id = sm.squad_id
            JOIN agent a ON a.id = sm.member_id
           WHERE s.workspace_id = $1
             AND sm.member_type = 'agent'
             AND a.workspace_id = $1
             AND a.owner_id     = sqlc.narg('involves_user_id')::uuid
    ))
  )
  AND (
    sqlc.narg('restrict_to_user')::uuid IS NULL
    OR (i.creator_type = 'member' AND i.creator_id = sqlc.narg('restrict_to_user')::uuid)
    OR (i.assignee_type = 'member' AND i.assignee_id = sqlc.narg('restrict_to_user')::uuid)
    OR (i.assignee_type = 'agent' AND i.assignee_id IN (
          SELECT a.id FROM agent a
           WHERE a.workspace_id = $1 AND a.owner_id = sqlc.narg('restrict_to_user')::uuid))
    OR (i.assignee_type = 'squad' AND i.assignee_id IN (
          SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id
           WHERE s.workspace_id = $1 AND sm.member_type = 'member'
             AND sm.member_id = sqlc.narg('restrict_to_user')::uuid
          UNION
          SELECT s.id FROM squad s JOIN agent a ON a.id = s.leader_id
           WHERE s.workspace_id = $1 AND a.owner_id = sqlc.narg('restrict_to_user')::uuid
          UNION
          SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id
            JOIN agent a ON a.id = sm.member_id
           WHERE s.workspace_id = $1 AND sm.member_type = 'agent'
             AND a.owner_id = sqlc.narg('restrict_to_user')::uuid))
  );

-- name: CountIssuesByStatus :many
-- CountIssues split by status: the same predicates, line for line, with a
-- GROUP BY on top. It exists so a capped page of rows can travel with EXACT
-- per-status numbers — the assistant's list_my_issues reports these as
-- by_status (and their sum as total), and "how many of mine are done" is then
-- read off an aggregate instead of counted from a truncated page.
--
-- Keep it in lockstep with CountIssues and ListIssues. The sum over every row
-- this returns MUST equal CountIssues for the same arguments.
SELECT i.status, count(*)::bigint AS count FROM issue i
WHERE i.workspace_id = $1
  AND (sqlc.narg('include_archived')::bool IS TRUE OR i.archived_at IS NULL)
  AND (sqlc.narg('status')::text IS NULL OR i.status = sqlc.narg('status'))
  AND (sqlc.narg('priority')::text IS NULL OR i.priority = sqlc.narg('priority'))
  AND (sqlc.narg('assignee_id')::uuid IS NULL OR i.assignee_id = sqlc.narg('assignee_id'))
  AND (sqlc.narg('assignee_ids')::uuid[] IS NULL OR i.assignee_id = ANY(sqlc.narg('assignee_ids')::uuid[]))
  AND (sqlc.narg('creator_id')::uuid IS NULL OR i.creator_id = sqlc.narg('creator_id'))
  AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('scheduled')::bool IS NULL OR (i.start_date IS NOT NULL OR i.due_date IS NOT NULL))
  AND (sqlc.narg('metadata_filter')::jsonb IS NULL OR i.metadata @> sqlc.narg('metadata_filter')::jsonb)
  AND (
    sqlc.narg('involves_user_id')::uuid IS NULL
    OR (i.assignee_type = 'agent' AND i.assignee_id IN (
          SELECT a.id FROM agent a
           WHERE a.workspace_id = $1
             AND a.owner_id     = sqlc.narg('involves_user_id')::uuid
    ))
    OR (i.assignee_type = 'squad' AND i.assignee_id IN (
          SELECT sm.squad_id
            FROM squad_member sm
            JOIN squad s ON s.id = sm.squad_id
           WHERE s.workspace_id = $1
             AND sm.member_type = 'member'
             AND sm.member_id   = sqlc.narg('involves_user_id')::uuid
          UNION
          SELECT s.id
            FROM squad s
            JOIN agent a ON a.id = s.leader_id
           WHERE s.workspace_id = $1
             AND a.workspace_id = $1
             AND a.owner_id     = sqlc.narg('involves_user_id')::uuid
          UNION
          SELECT sm.squad_id
            FROM squad_member sm
            JOIN squad s ON s.id = sm.squad_id
            JOIN agent a ON a.id = sm.member_id
           WHERE s.workspace_id = $1
             AND sm.member_type = 'agent'
             AND a.workspace_id = $1
             AND a.owner_id     = sqlc.narg('involves_user_id')::uuid
    ))
  )
  AND (
    sqlc.narg('restrict_to_user')::uuid IS NULL
    OR (i.creator_type = 'member' AND i.creator_id = sqlc.narg('restrict_to_user')::uuid)
    OR (i.assignee_type = 'member' AND i.assignee_id = sqlc.narg('restrict_to_user')::uuid)
    OR (i.assignee_type = 'agent' AND i.assignee_id IN (
          SELECT a.id FROM agent a
           WHERE a.workspace_id = $1 AND a.owner_id = sqlc.narg('restrict_to_user')::uuid))
    OR (i.assignee_type = 'squad' AND i.assignee_id IN (
          SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id
           WHERE s.workspace_id = $1 AND sm.member_type = 'member'
             AND sm.member_id = sqlc.narg('restrict_to_user')::uuid
          UNION
          SELECT s.id FROM squad s JOIN agent a ON a.id = s.leader_id
           WHERE s.workspace_id = $1 AND a.owner_id = sqlc.narg('restrict_to_user')::uuid
          UNION
          SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id
            JOIN agent a ON a.id = sm.member_id
           WHERE s.workspace_id = $1 AND sm.member_type = 'agent'
             AND a.owner_id = sqlc.narg('restrict_to_user')::uuid))
  )
GROUP BY i.status
ORDER BY i.status;

-- name: ListChildIssues :many
SELECT * FROM issue
WHERE parent_issue_id = $1
ORDER BY position ASC, created_at DESC;

-- name: ListChildrenByParents :many
-- Batched variant of ListChildIssues: returns all children for the given
-- parent set in one round trip. Used by Swimlane to avoid an N+1 fan-out
-- (one request per visible parent lane). Result is grouped client-side by
-- parent_issue_id; the workspace filter is also enforced so callers can't
-- enumerate children of parents in workspaces they don't belong to.
SELECT i.* FROM issue i
WHERE i.workspace_id = sqlc.arg('workspace_id')
  AND i.parent_issue_id = ANY(sqlc.arg('parent_ids')::uuid[])
  AND (
    sqlc.narg('restrict_to_user')::uuid IS NULL
    OR (i.creator_type = 'member' AND i.creator_id = sqlc.narg('restrict_to_user')::uuid)
    OR (i.assignee_type = 'member' AND i.assignee_id = sqlc.narg('restrict_to_user')::uuid)
    OR (i.assignee_type = 'agent' AND i.assignee_id IN (
          SELECT a.id FROM agent a
           WHERE a.workspace_id = sqlc.arg('workspace_id') AND a.owner_id = sqlc.narg('restrict_to_user')::uuid))
    OR (i.assignee_type = 'squad' AND i.assignee_id IN (
          SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id
           WHERE s.workspace_id = sqlc.arg('workspace_id') AND sm.member_type = 'member' AND sm.member_id = sqlc.narg('restrict_to_user')::uuid
          UNION SELECT s.id FROM squad s JOIN agent a ON a.id = s.leader_id
           WHERE s.workspace_id = sqlc.arg('workspace_id') AND a.owner_id = sqlc.narg('restrict_to_user')::uuid
          UNION SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id JOIN agent a ON a.id = sm.member_id
           WHERE s.workspace_id = sqlc.arg('workspace_id') AND sm.member_type = 'agent' AND a.owner_id = sqlc.narg('restrict_to_user')::uuid))
  )
ORDER BY i.parent_issue_id, i.position ASC, i.created_at DESC;

-- name: GetIssueByOrigin :one
-- Finds the issue stamped with a specific (origin_type, origin_id) pair.
-- Used by quick-create completion to deterministically locate the issue
-- produced by a given agent_task_queue.id — robust against concurrent
-- issue creates by the same agent (assignment task + quick-create both
-- running with max_concurrent_tasks > 1).
SELECT * FROM issue
WHERE workspace_id = $1
  AND origin_type = $2
  AND origin_id = $3
LIMIT 1;

-- name: CountCreatedIssueAssignees :many
-- Count assignees on issues created by a specific user.
SELECT
  assignee_type,
  assignee_id,
  COUNT(*)::bigint as frequency
FROM issue
WHERE workspace_id = $1
  AND creator_id = $2
  AND creator_type = 'member'
  AND assignee_type IS NOT NULL
  AND assignee_id IS NOT NULL
GROUP BY assignee_type, assignee_id;

-- name: ChildIssueProgress :many
SELECT i.parent_issue_id,
       COUNT(*)::bigint AS total,
       COUNT(*) FILTER (WHERE i.status IN ('done', 'cancelled'))::bigint AS done
FROM issue i
WHERE i.workspace_id = sqlc.arg('workspace_id')
  AND i.parent_issue_id IS NOT NULL
  AND (
    sqlc.narg('restrict_to_user')::uuid IS NULL
    OR (i.creator_type = 'member' AND i.creator_id = sqlc.narg('restrict_to_user')::uuid)
    OR (i.assignee_type = 'member' AND i.assignee_id = sqlc.narg('restrict_to_user')::uuid)
    OR (i.assignee_type = 'agent' AND i.assignee_id IN (
          SELECT a.id FROM agent a
           WHERE a.workspace_id = sqlc.arg('workspace_id') AND a.owner_id = sqlc.narg('restrict_to_user')::uuid))
    OR (i.assignee_type = 'squad' AND i.assignee_id IN (
          SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id
           WHERE s.workspace_id = sqlc.arg('workspace_id') AND sm.member_type = 'member' AND sm.member_id = sqlc.narg('restrict_to_user')::uuid
          UNION SELECT s.id FROM squad s JOIN agent a ON a.id = s.leader_id
           WHERE s.workspace_id = sqlc.arg('workspace_id') AND a.owner_id = sqlc.narg('restrict_to_user')::uuid
          UNION SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id JOIN agent a ON a.id = sm.member_id
           WHERE s.workspace_id = sqlc.arg('workspace_id') AND sm.member_type = 'agent' AND a.owner_id = sqlc.narg('restrict_to_user')::uuid))
  )
GROUP BY i.parent_issue_id;

-- SearchIssues: moved to handler (dynamic SQL for multi-word search support).

-- name: SetIssueMetadataKey :one
-- Atomically sets a single key in the issue's metadata JSONB. The
-- workspace_id filter is the authorization gate — handler resolves the
-- issue first so this is also the tenant check.
UPDATE issue SET
    metadata = jsonb_set(metadata, ARRAY[sqlc.arg('key')::text], sqlc.arg('value')::jsonb),
    updated_at = now()
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id')
RETURNING *;

-- name: DeleteIssueMetadataKey :one
-- Atomically removes a single key from the issue's metadata JSONB.
-- Deleting a missing key is a no-op (still returns the row).
UPDATE issue SET
    metadata = metadata - sqlc.arg('key')::text,
    updated_at = now()
WHERE id = sqlc.arg('id') AND workspace_id = sqlc.arg('workspace_id')
RETURNING *;

-- name: MarkIssueFirstExecuted :one
-- Flips first_executed_at from NULL to now() atomically. Returns the row if
-- this was the first time the issue was executed; no rows otherwise. The
-- analytics issue_executed event fires exactly when this returns a row —
-- retries and re-assignments hit the WHERE clause and no-op.
UPDATE issue
SET first_executed_at = now()
WHERE id = $1 AND first_executed_at IS NULL
RETURNING id, workspace_id, creator_type, creator_id, first_executed_at;

-- name: ListStaleIssues :many
-- LIVING TRUTH, Tier 2 (docs/living-truth-plan.md): the issues whose tracker
-- state has most likely stopped being true. This query NEVER writes — a stale
-- signal is inference, so it renders and nothing more.
--
-- Four rules, each on a disjoint status, so an issue matches at most one and a
-- plain CASE is enough (no priority ordering to get wrong):
--
--   idle           in_progress · no active task · no open linked PR ·
--                  no activity for @idle_days
--   review_done    in_review · has linked PRs · every one merged/closed, and
--                  the last of them resolved @review_done_days ago
--   reopened_work  done · at least one linked PR still open/draft
--   blocked_quiet  blocked · no activity for @blocked_days
--
-- "Activity" is the freshest of three clocks — the issue row, its newest
-- comment, and its newest agent task. agent_task_queue has no updated_at, so
-- the task clock is the latest of the lifecycle stamps it does carry.
--
-- `since` is the timestamp the matching rule measured FROM, so the caller can
-- render an age without a second query: last activity for the two quiet rules,
-- the moment the last PR resolved for review_done, and when the still-open PR
-- was opened for reopened_work.
--
-- Shape / EXPLAIN sanity: the candidate CTE narrows to one workspace's
-- non-archived issues in the four interesting statuses FIRST (idx on
-- issue(workspace_id) + the status predicate), and the three LATERALs then run
-- once per surviving row against indexed foreign keys — comment(issue_id),
-- agent_task_queue(issue_id), issue_pull_request(issue_id) (PK prefix) joined
-- to github_pull_request by primary key. That is N small index lookups over a
-- board-sized candidate set, not a workspace-wide scan of comments or tasks,
-- which is why this stays a SEPARATE query instead of a join into the hot list
-- path.
WITH candidate AS (
    SELECT i.id, i.number, i.title, i.status, i.updated_at
    FROM issue i
    WHERE i.workspace_id = sqlc.arg('workspace_id')
      AND i.archived_at IS NULL
      AND i.status IN ('in_progress', 'in_review', 'done', 'blocked')
      AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id')::uuid)
      -- Same non-owner visibility gate as ListIssues: a restricted member sees
      -- only issues that are theirs. NULL disables it (owners see everything).
      AND (
        sqlc.narg('restrict_to_user')::uuid IS NULL
        OR (i.creator_type = 'member' AND i.creator_id = sqlc.narg('restrict_to_user')::uuid)
        OR (i.assignee_type = 'member' AND i.assignee_id = sqlc.narg('restrict_to_user')::uuid)
        OR (i.assignee_type = 'agent' AND i.assignee_id IN (
              SELECT a.id FROM agent a
               WHERE a.workspace_id = sqlc.arg('workspace_id')
                 AND a.owner_id = sqlc.narg('restrict_to_user')::uuid))
        OR (i.assignee_type = 'squad' AND i.assignee_id IN (
              SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id
               WHERE s.workspace_id = sqlc.arg('workspace_id') AND sm.member_type = 'member'
                 AND sm.member_id = sqlc.narg('restrict_to_user')::uuid
              UNION
              SELECT s.id FROM squad s JOIN agent a ON a.id = s.leader_id
               WHERE s.workspace_id = sqlc.arg('workspace_id')
                 AND a.owner_id = sqlc.narg('restrict_to_user')::uuid
              UNION
              SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id
                JOIN agent a ON a.id = sm.member_id
               WHERE s.workspace_id = sqlc.arg('workspace_id') AND sm.member_type = 'agent'
                 AND a.owner_id = sqlc.narg('restrict_to_user')::uuid))
      )
),
signal AS (
    SELECT
        c.id,
        c.number,
        c.title,
        c.status,
        GREATEST(
            c.updated_at,
            COALESCE(cm.last_comment_at, c.updated_at),
            COALESCE(tk.last_task_at, c.updated_at)
        ) AS last_activity_at,
        COALESCE(tk.has_active_task, FALSE) AS has_active_task,
        COALESCE(pr.linked_count, 0) AS linked_count,
        COALESCE(pr.open_count, 0) AS open_count,
        pr.resolved_at,
        pr.open_since
    FROM candidate c
    LEFT JOIN LATERAL (
        SELECT MAX(cc.created_at) AS last_comment_at
        FROM comment cc
        WHERE cc.issue_id = c.id
    ) cm ON TRUE
    LEFT JOIN LATERAL (
        SELECT
            MAX(GREATEST(
                t.created_at,
                COALESCE(t.dispatched_at, t.created_at),
                COALESCE(t.started_at, t.created_at),
                COALESCE(t.completed_at, t.created_at)
            )) AS last_task_at,
            bool_or(t.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory')) AS has_active_task
        FROM agent_task_queue t
        WHERE t.issue_id = c.id
    ) tk ON TRUE
    LEFT JOIN LATERAL (
        SELECT
            count(*) AS linked_count,
            COALESCE(SUM(CASE WHEN p.state IN ('open', 'draft') THEN 1 ELSE 0 END), 0) AS open_count,
            -- The moment the LAST linked PR stopped being in flight.
            MAX(COALESCE(p.merged_at, p.closed_at)) AS resolved_at,
            -- The newest still-open PR: the freshest evidence that work resumed.
            MAX(CASE WHEN p.state IN ('open', 'draft') THEN p.pr_created_at END) AS open_since
        FROM issue_pull_request ipr
        JOIN github_pull_request p ON p.id = ipr.pull_request_id
        WHERE ipr.issue_id = c.id
    ) pr ON TRUE
),
verdict AS (
    SELECT
        s.id,
        s.number,
        s.title,
        s.status,
        CASE
            WHEN s.status = 'in_progress'
                 AND NOT s.has_active_task
                 AND s.open_count = 0
                 AND s.last_activity_at <= now() - (sqlc.arg('idle_days')::int * INTERVAL '1 day')
                THEN 'idle'
            WHEN s.status = 'in_review'
                 AND s.linked_count > 0
                 AND s.open_count = 0
                 -- A merged/closed PR with neither timestamp cannot be aged, so
                 -- it fails closed (no signal) rather than reporting an age we
                 -- would have had to invent.
                 AND s.resolved_at IS NOT NULL
                 AND s.resolved_at <= now() - (sqlc.arg('review_done_days')::int * INTERVAL '1 day')
                THEN 'review_done'
            WHEN s.status = 'done' AND s.open_count > 0
                THEN 'reopened_work'
            WHEN s.status = 'blocked'
                 AND s.last_activity_at <= now() - (sqlc.arg('blocked_days')::int * INTERVAL '1 day')
                THEN 'blocked_quiet'
            ELSE NULL
        END AS reason,
        CASE
            WHEN s.status = 'in_review' THEN COALESCE(s.resolved_at, s.last_activity_at)
            WHEN s.status = 'done' THEN COALESCE(s.open_since, s.last_activity_at)
            ELSE s.last_activity_at
        END AS since
    FROM signal s
)
SELECT
    v.id AS issue_id,
    v.number,
    v.title,
    v.status,
    v.reason::text AS reason,
    v.since::timestamptz AS since
FROM verdict v
WHERE v.reason IS NOT NULL
ORDER BY v.since ASC, v.number ASC;
