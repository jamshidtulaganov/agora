-- Per-workspace Zoho connection: one OAuth client + refresh token covering the
-- whole Zoho suite (scope superset), replacing global env-var credentials for
-- the dynamic integration path (docs/zoho-dynamic-integration.md §1.1).
-- client_secret and refresh_token are sealed at rest with a secretbox loaded
-- from AGORA_ZOHO_SECRET_KEY; plaintext is only ever decrypted server-side
-- (discovery proxy, sync engine, probes) and never returned by any endpoint.
-- dc is load-bearing: every Zoho host (accounts, CRM, Desk, Projects, Sprints)
-- derives from it — never hardcode .com.
CREATE TABLE zoho_connection (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id             uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    dc                       text NOT NULL DEFAULT 'us',
    client_id                text NOT NULL,
    client_secret_encrypted  bytea NOT NULL,
    refresh_token_encrypted  bytea NOT NULL,
    scopes                   text NOT NULL DEFAULT '',  -- granted scope list, for re-consent detection
    crm_org_id               text NOT NULL DEFAULT '',
    desk_org_id              text NOT NULL DEFAULT '',
    projects_portal_id       text NOT NULL DEFAULT '',
    sprints_team_id          text NOT NULL DEFAULT '',
    probe_status             text NOT NULL DEFAULT '',  -- 'ok' | 'invalid' | 'unreachable'
    probed_at                timestamptz,
    created_by               uuid REFERENCES "user"(id) ON DELETE SET NULL,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id)
);
