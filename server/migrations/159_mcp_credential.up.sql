-- Per-workspace sealed auth for REMOTE MCP servers (dynamic MCP core,
-- docs/decoupling-manifest.md Tier 2 #6). A remote MCP server entry in an
-- agent's mcp_config is `{"type":"http","url":"…","headers":{…}}`; the headers
-- almost always carry a bearer token. That token is a capability (possession
-- exfiltrates whatever the tool exposes), so it must NOT sit plaintext in the
-- unsealed `agent.mcp_config` column. It lives here instead, sealed at rest,
-- and is merged into the entry's `headers` server-side at task dispatch
-- (injectMcpCredentials) — the exact pattern git_credential / figma_credential
-- use for their tokens.
--
-- Keyed by (workspace_id, server_name): server_name matches the
-- `mcpServers[<name>]` key, so the whole workspace shares one sealed credential
-- for a given remote server (e.g. every agent's "linear" entry resolves the
-- same token). secret_encrypted holds a secretbox-sealed JSON blob
-- (`{"headers":{…}}`) sealed with a key loaded from AGORA_MCP_SECRET_KEY; the
-- write endpoints fail closed (503) when that key is unset rather than store a
-- token in plaintext. The plaintext is only ever decrypted server-side to hand
-- to the authenticated daemon.
CREATE TABLE IF NOT EXISTS mcp_credential (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id     uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    server_name      text NOT NULL,                      -- matches mcpServers[<name>]
    secret_encrypted bytea NOT NULL,                     -- secretbox-sealed {"headers":{…}} blob
    secret_last4     text NOT NULL DEFAULT '',           -- last 4 chars of the sealed token, for a UI hint (never the token)
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    created_by       uuid REFERENCES "user"(id) ON DELETE SET NULL
);

-- One sealed credential per (workspace, server_name): the dispatcher resolves a
-- remote server entry to a single credential by its name. Rotating a token is
-- an upsert on this key.
CREATE UNIQUE INDEX IF NOT EXISTS mcp_credential_ws_server_idx
    ON mcp_credential (workspace_id, server_name);
