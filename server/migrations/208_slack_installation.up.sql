-- Slack app integration, Phase 1 (docs/slack-integration-plan.md §5).
--
-- This is the Slack *app* (OAuth v2 bot token, Events API, Block Kit) and is
-- deliberately disjoint from the existing Slack *Incoming Webhook* release
-- connector (release_integration, migration 158), which keeps `release:shipped`
-- and `deploy:recorded` for itself. The two paths address different event sets,
-- so there is nothing to migrate and no double-post to reconcile.
--
-- Credential handling mirrors lark_installation / telegram_installation: the
-- bot token is sealed by the application layer with AGORA_SLACK_SECRET_KEY
-- (internal/util/secretbox) before it reaches the DB. A dump leaks ciphertext
-- only, and the install path fails closed (503) when the key is unset rather
-- than falling back to plaintext storage.

-- =====================
-- slack_installation
-- =====================
-- One row per (Agora workspace, Slack team). NOT unique on team_id alone:
-- one Slack workspace may host several Agora workspaces (an agency, or a
-- company running one Agora workspace per product). Slack issues one bot
-- token per (app, team), so such sibling rows legitimately carry the same
-- token — which is why revoking one row must NOT call apps.uninstall while a
-- sibling row for the same team_id is still 'active'.
CREATE TABLE slack_installation (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id        UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    -- Slack team (workspace) this install belongs to, e.g. "T01234567".
    team_id             TEXT NOT NULL,
    team_name           TEXT NOT NULL DEFAULT '',
    -- Enterprise Grid org id when the install is org-level; '' otherwise.
    enterprise_id       TEXT NOT NULL DEFAULT '',
    app_id              TEXT NOT NULL,
    bot_user_id         TEXT NOT NULL,
    -- Ciphertext of the xoxb- bot token (secretbox, AGORA_SLACK_SECRET_KEY).
    -- A bot token can post to every channel the app is in, so it is never
    -- stored in plaintext, never logged, and never returned by any endpoint.
    bot_token_encrypted BYTEA NOT NULL,
    -- Comma-separated scope list exactly as Slack returned it. Kept so the UI
    -- can tell an operator when a re-install is needed after we add a scope.
    scopes              TEXT NOT NULL DEFAULT '',
    installer_user_id   UUID NOT NULL REFERENCES "user"(id) ON DELETE RESTRICT,
    status              TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'revoked')),
    installed_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Re-installing the same Slack team into the same Agora workspace
    -- refreshes the row (token rotation) instead of accumulating stale tokens.
    UNIQUE (workspace_id, team_id),
    -- Composite-FK target (lark_installation's trick): every child row carries
    -- (installation_id, workspace_id) so it cannot claim a workspace its
    -- installation does not belong to.
    UNIQUE (id, workspace_id)
);

CREATE INDEX idx_slack_installation_workspace ON slack_installation(workspace_id);
-- app_uninstalled / tokens_revoked arrive with a team id and no workspace
-- context, so the ingress path looks up every row for a team.
CREATE INDEX idx_slack_installation_team ON slack_installation(team_id)
    WHERE status = 'active';

-- =====================
-- slack_channel_route
-- =====================
-- Where a workspace's notifications go. Slack routing is CONFIGURED, not
-- derived (a Slack app belongs to the workspace, unlike a Telegram bot which
-- belongs to an agent), so an admin picks channels and the events each hears.
-- `events TEXT[]` is deliberately the same shape as release_integration.events
-- (migration 158) so the matcher stays a pure, DB-free predicate.
CREATE TABLE slack_channel_route (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL,
    installation_id UUID NOT NULL,
    channel_id      TEXT NOT NULL,
    channel_name    TEXT NOT NULL DEFAULT '',
    -- NULL = every project in the workspace.
    project_id      UUID REFERENCES project(id) ON DELETE CASCADE,
    events          TEXT[] NOT NULL DEFAULT '{}',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by      UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (installation_id, workspace_id)
        REFERENCES slack_installation(id, workspace_id) ON DELETE CASCADE,
    -- NULLS NOT DISTINCT (PG15+; we run pg17) is required — without it a NULL
    -- project_id would let the same channel be routed twice.
    UNIQUE NULLS NOT DISTINCT (installation_id, channel_id, project_id)
);

CREATE INDEX idx_slack_channel_route_workspace ON slack_channel_route(workspace_id)
    WHERE enabled;
CREATE INDEX idx_slack_channel_route_installation ON slack_channel_route(installation_id);

-- =====================
-- user_external_identity: slack
-- =====================
-- One Slack identity per user, migration 186's shape. The installer is bound
-- during the OAuth callback (authed_user.id from the oauth.v2.access response)
-- and personal links reuse the same provider row.
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_external_identity_slack_user
    ON user_external_identity (user_id)
    WHERE provider = 'slack';
