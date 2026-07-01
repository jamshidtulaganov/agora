-- name: ListMembers :many
SELECT * FROM member
WHERE workspace_id = $1
ORDER BY created_at ASC;

-- name: GetMember :one
SELECT * FROM member
WHERE id = $1;

-- name: GetMemberByUserAndWorkspace :one
SELECT * FROM member
WHERE user_id = $1 AND workspace_id = $2;

-- name: CreateMember :one
INSERT INTO member (workspace_id, user_id, role)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateMemberRole :one
UPDATE member SET role = $2
WHERE id = $1
RETURNING *;

-- name: DeleteMember :exec
DELETE FROM member WHERE id = $1;

-- name: ListMembersWithUser :many
SELECT m.id, m.workspace_id, m.user_id, m.role, m.created_at,
       u.name as user_name, u.email as user_email, u.avatar_url as user_avatar_url
FROM member m
JOIN "user" u ON u.id = m.user_id
WHERE m.workspace_id = $1
ORDER BY m.created_at ASC;

-- name: ListWorkspaceActorDirectory :many
-- Display info (name, avatar) for every user referenced in a workspace: comment
-- authors, issue assignees/creators, and members. Lets the UI render a real
-- name + avatar for people who are no longer (or never were) team members —
-- e.g. an imported Bitrix comment's author who is not in the team. Display only;
-- the member list still gates pickers and assignment.
SELECT DISTINCT u.id, u.name, u.avatar_url
FROM "user" u
WHERE u.id IN (
    SELECT c.author_id FROM comment c
      WHERE c.workspace_id = $1 AND c.author_type = 'member' AND c.author_id IS NOT NULL
    UNION
    SELECT i.assignee_id FROM issue i
      WHERE i.workspace_id = $1 AND i.assignee_type = 'member' AND i.assignee_id IS NOT NULL
    UNION
    SELECT i.creator_id FROM issue i
      WHERE i.workspace_id = $1 AND i.creator_type = 'member' AND i.creator_id IS NOT NULL
);
