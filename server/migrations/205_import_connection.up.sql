-- Per-workspace import connection: the sealed credential an importer uses to
-- read a source tracker (Linear personal API key first; Jira site + email +
-- token next). One row per (workspace, source, label) so a team can hold two
-- Linear keys or two Jira sites without either overwriting the other.
--
-- secret_encrypted holds a secretbox-sealed (AES-256-GCM) token, keyed from
-- AGORA_IMPORT_SECRET_KEY. The write path fails closed — if the key is unset
-- the endpoint answers 503 rather than storing plaintext — exactly as
-- git_credential (migration 132) and zoho_connection (migration 142) do.
-- Plaintext is decrypted server-side only, inside the adapter's HTTP client,
-- and is never returned by an endpoint, logged, put on an event, or reachable
-- by the assistant (its import tools take a connection_id and nothing else).
CREATE TABLE import_connection (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id     uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    source           text NOT NULL,                  -- linear | jira | asana | clickup | trello | csv
    label            text NOT NULL DEFAULT '',       -- operator-facing name; '' is the default connection
    base_url         text NOT NULL DEFAULT '',       -- Jira site URL; empty for Linear
    account_email    text NOT NULL DEFAULT '',       -- Jira basic-auth email; empty for Linear
    secret_encrypted bytea NOT NULL,                 -- secretbox-sealed API token / refresh token
    scopes           text NOT NULL DEFAULT '',       -- granted scope list, for re-consent detection
    probe_status     text NOT NULL DEFAULT '',       -- '' | ok | invalid | unreachable
    probed_at        timestamptz,
    created_by       uuid REFERENCES "user"(id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, source, label)
);

-- Listing a workspace's connections is the only hot read; the unique index
-- above already covers (workspace_id, ...) prefix lookups, so no extra index.
