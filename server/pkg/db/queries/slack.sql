-- Slack app integration (docs/slack-integration-plan.md §5). The tables are
-- defined in server/migrations/208_slack_installation.up.sql.
--
-- Scoping convention, same as lark.sql: every HTTP-reachable read goes through
-- a workspace-scoped variant. The team-scoped lookups exist only for the
-- Events API ingress, where Slack hands us a team id and no workspace context;
-- they are never reachable from a workspace-scoped route.

-- =====================
-- slack_installation
-- =====================

-- name: UpsertSlackInstallation :one
-- The OAuth v2 callback's only write. `bot_token_encrypted` is secretbox
-- ciphertext produced by the handler — never plaintext. Re-installing the same
-- Slack team into the same Agora workspace rotates the token in place and
-- flips status back to 'active'; accumulating rows would leave stale tokens
-- that each still believe they speak for this workspace.
INSERT INTO slack_installation (
    workspace_id, team_id, team_name, enterprise_id, app_id,
    bot_user_id, bot_token_encrypted, scopes, installer_user_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
)
ON CONFLICT (workspace_id, team_id) DO UPDATE SET
    team_name           = EXCLUDED.team_name,
    enterprise_id       = EXCLUDED.enterprise_id,
    app_id              = EXCLUDED.app_id,
    bot_user_id         = EXCLUDED.bot_user_id,
    bot_token_encrypted = EXCLUDED.bot_token_encrypted,
    scopes              = EXCLUDED.scopes,
    installer_user_id   = EXCLUDED.installer_user_id,
    status              = 'active',
    installed_at        = now(),
    updated_at          = now()
RETURNING *;

-- name: GetSlackInstallationInWorkspace :one
-- Workspace-scoped lookup: a forged installation id from another workspace
-- returns no rows instead of leaking existence.
SELECT * FROM slack_installation
WHERE id = $1 AND workspace_id = $2;

-- name: GetSlackInstallationForTeam :one
-- Outbound resolution for a known workspace (unfurl, notify): which install
-- speaks for this Agora workspace on this Slack team.
SELECT * FROM slack_installation
WHERE workspace_id = $1 AND team_id = $2 AND status = 'active';

-- name: ListSlackInstallationsByWorkspace :many
-- Active and revoked, newest first — status lets the UI distinguish "wired up"
-- from "torn down but kept for audit".
SELECT * FROM slack_installation
WHERE workspace_id = $1
ORDER BY installed_at DESC;

-- name: ListActiveSlackInstallationsByTeam :many
-- Events API ingress only: app_uninstalled / tokens_revoked carry a team id
-- and no workspace, and one Slack team may back several Agora workspaces.
SELECT * FROM slack_installation
WHERE team_id = $1 AND status = 'active'
ORDER BY installed_at;

-- name: CountOtherActiveSlackInstallationsForTeam :one
-- "Only the last row standing uninstalls": before calling apps.uninstall for a
-- revoked row, check whether a sibling Agora workspace still shares this
-- team's bot token.
SELECT count(*) FROM slack_installation
WHERE team_id = $1 AND id <> $2 AND status = 'active';

-- name: SetSlackInstallationStatus :exec
UPDATE slack_installation
SET status = $2, updated_at = now()
WHERE id = $1;

-- name: RevokeSlackInstallationsForTeam :execrows
-- app_uninstalled / tokens_revoked: the bot token for this team is dead, so
-- every Agora workspace installed on it stops. Rows are marked, not deleted,
-- so the audit trail survives and a re-install flips them back.
UPDATE slack_installation
SET status = 'revoked', updated_at = now()
WHERE team_id = $1 AND status = 'active';

-- name: DeleteSlackInstallation :exec
-- Workspace-scoped hard delete (used by tests and by an admin removing an
-- install outright). Routes cascade via the composite FK.
DELETE FROM slack_installation
WHERE id = $1 AND workspace_id = $2;

-- =====================
-- slack_channel_route
-- =====================

-- name: UpsertSlackChannelRoute :one
-- Conflict inference matches the NULLS NOT DISTINCT unique index, so a route
-- with a NULL project_id (all projects) cannot be created twice for the same
-- channel.
INSERT INTO slack_channel_route (
    workspace_id, installation_id, channel_id, channel_name,
    project_id, events, enabled, created_by
) VALUES (
    $1, $2, $3, $4, sqlc.narg('project_id'), $5, $6, sqlc.narg('created_by')
)
ON CONFLICT (installation_id, channel_id, project_id) DO UPDATE SET
    channel_name = EXCLUDED.channel_name,
    events       = EXCLUDED.events,
    enabled      = EXCLUDED.enabled,
    updated_at   = now()
RETURNING *;

-- name: GetSlackChannelRouteInWorkspace :one
SELECT * FROM slack_channel_route
WHERE id = $1 AND workspace_id = $2;

-- name: ListSlackChannelRoutesByWorkspace :many
SELECT * FROM slack_channel_route
WHERE workspace_id = $1
ORDER BY created_at;

-- name: ListEnabledSlackChannelRoutesByWorkspace :many
-- The delivery path's only read. Event matching stays in Go (a pure predicate
-- over the events array) so it is unit-testable without a database.
SELECT * FROM slack_channel_route
WHERE workspace_id = $1 AND enabled
ORDER BY created_at;

-- name: DeleteSlackChannelRoute :exec
DELETE FROM slack_channel_route
WHERE id = $1 AND workspace_id = $2;

-- name: DeleteSlackChannelRoutesForInstallation :exec
-- Revoking an installation drops its routes but keeps the row for audit, so
-- re-installing does not resurrect channels the admin has since abandoned.
DELETE FROM slack_channel_route
WHERE installation_id = $1 AND workspace_id = $2;
