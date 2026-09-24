-- A person's own Zoho account, connected once through Zoho's consent screen
-- and used in every workspace. Every Zoho read made for that person — by the
-- Assistant or by an agent working for them — uses this grant, so Zoho's own
-- role, profile and sharing rules decide what Agora sees. The grant only
-- carries read scopes. Supersedes the per-workspace zoho_user_binding
-- (pasted self-client codes); that table is left in place, unused, and
-- dropped in a later migration.
CREATE TABLE zoho_account (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                 uuid NOT NULL UNIQUE REFERENCES "user"(id) ON DELETE CASCADE,
    dc                      text NOT NULL,
    refresh_token_encrypted bytea NOT NULL,
    scopes                  text NOT NULL DEFAULT '',
    zoho_email              text NOT NULL DEFAULT '',
    zoho_name               text NOT NULL DEFAULT '',
    crm_user_id             text NOT NULL DEFAULT '',
    crm_role                text NOT NULL DEFAULT '',
    crm_profile             text NOT NULL DEFAULT '',
    desk_org_id             text NOT NULL DEFAULT '',
    desk_agent_id           text NOT NULL DEFAULT '',
    desk_departments        jsonb NOT NULL DEFAULT '[]'::jsonb, -- department names
    status                  text NOT NULL DEFAULT 'connected'
        CHECK (status IN ('connected', 'reconnect')),
    checked_at              timestamptz,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now()
);

-- One pending "Connect Zoho" attempt: the random state sent to Zoho and back,
-- single use, honoured for 15 minutes.
CREATE TABLE zoho_oauth_state (
    state      text PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- One row per Zoho read, for audit: who, from where, what, how many — never
-- the record contents.
CREATE TABLE zoho_call_log (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    workspace_id uuid REFERENCES workspace(id) ON DELETE SET NULL,
    source       text NOT NULL CHECK (source IN ('agent', 'assistant')),
    task_id      uuid,
    tool         text NOT NULL,
    object       text NOT NULL DEFAULT '',
    record_count int NOT NULL DEFAULT 0,
    duration_ms  int NOT NULL DEFAULT 0,
    error        text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_zoho_call_log_workspace ON zoho_call_log (workspace_id, created_at DESC);
CREATE INDEX idx_zoho_call_log_user ON zoho_call_log (user_id, created_at DESC);
