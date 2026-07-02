-- Per-user Zoho identity binding (docs/zoho-dynamic-integration.md U-track):
-- maps an Agora user to their OWN Zoho account grant so agents acting for
-- that user call Zoho as that user — actions attribute to the real person
-- and Zoho role permissions follow them. The grant is minted under the
-- WORKSPACE connection's OAuth client (a Zoho refresh token is bound to the
-- (client, user) pair), so the binding is keyed per (workspace, user) and
-- cascades away with either the workspace connection or the user.
-- refresh_token is sealed with AGORA_ZOHO_SECRET_KEY (same box as
-- zoho_connection); plaintext never leaves the server.
CREATE TABLE zoho_user_binding (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id             uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    user_id                  uuid NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    connection_id            uuid NOT NULL REFERENCES zoho_connection(id) ON DELETE CASCADE,
    refresh_token_encrypted  bytea NOT NULL,
    scopes                   text NOT NULL DEFAULT '',
    zoho_user_email          text NOT NULL DEFAULT '',  -- best-effort identity hint from the probe
    probe_status             text NOT NULL DEFAULT '',  -- 'ok' | 'invalid' | 'unreachable'
    probed_at                timestamptz,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, user_id)
);

CREATE INDEX idx_zoho_user_binding_user ON zoho_user_binding(user_id);
