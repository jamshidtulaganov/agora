-- Per-workspace release integration (release-hub Thread B / Phase 2,
-- docs/release-hub-and-redesign.md). A workspace configures one or more
-- outbound connectors that fire on release-lifecycle events (deploy:recorded,
-- release:shipped). Phase 2 wires only kind='webhook' — a signed POST to an
-- arbitrary URL — but the column allows the named connectors
-- (slack|github_release|gitlab_release|sentry|bitrix) that land in Phase 3-4.
--
-- Split of routing vs secret mirrors git_credential / figma_credential /
-- zoho_connection: `config` holds NON-secret display/routing metadata (a
-- display name, event-filter defaults); the SECRET half — the webhook URL plus
-- an optional HMAC signing secret — is sealed at rest in `secret_encrypted`
-- with a secretbox loaded from AGORA_RELEASE_SECRET_KEY. The plaintext is only
-- ever decrypted server-side in the dispatcher (registerReleaseOutbound); no
-- endpoint returns it. For a webhook the URL itself is a capability (possession
-- exfiltrates release data), so it lives in the sealed blob, not in `config`.
--
-- `events` names WHICH lifecycle events fire this integration, stored as the
-- short form the matcher uses ('deploy_recorded' / 'release_shipped'); an empty
-- array fires nothing. `probe_status` records the save-time reachability check
-- ('ok' | 'invalid' | 'unreachable' | '') so the UI can flag a URL that
-- rejected our test probe.
CREATE TABLE IF NOT EXISTS release_integration (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    kind              text NOT NULL,                     -- 'webhook' (Phase 2); slack|github_release|gitlab_release|sentry|bitrix later
    config            jsonb NOT NULL DEFAULT '{}',        -- non-secret display/routing metadata (name, filter defaults)
    secret_encrypted  bytea,                              -- secretbox-sealed {url, signing} blob
    events            text[] NOT NULL DEFAULT '{}',       -- lifecycle events that fire it: deploy_recorded / release_shipped
    enabled           boolean NOT NULL DEFAULT true,
    probe_status      text NOT NULL DEFAULT '',           -- 'ok' | 'invalid' | 'unreachable' | ''
    created_by        uuid REFERENCES "user"(id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- The dispatcher's hot path is "enabled integrations for this workspace".
CREATE INDEX IF NOT EXISTS idx_release_integration_workspace_enabled
    ON release_integration (workspace_id, enabled);
