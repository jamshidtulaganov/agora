-- Per-workspace git credentials: lets one Agora workspace clone private repos
-- across several git accounts (e.g. two GitHub accounts for two companies). The
-- daemon matches a repo's host+owner to exactly one credential and clones with
-- it, instead of relying on a single ambient gh-active account.
--
-- secret_encrypted holds a secretbox-sealed PAT (auth_kind='token'); SSH keys
-- (auth_kind='ssh') can reuse the same column later. The plaintext secret is
-- only ever decrypted server-side to hand to the authenticated daemon.
CREATE TABLE git_credential (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id     uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    label            text NOT NULL,
    provider         text NOT NULL DEFAULT 'github',     -- github | gitlab
    host             text NOT NULL DEFAULT 'github.com', -- matched against the repo URL host
    owner            text NOT NULL,                      -- org/user the credential covers (disambiguates accounts on one host)
    username         text NOT NULL DEFAULT '',           -- the account login used for git auth
    auth_kind        text NOT NULL DEFAULT 'token',      -- token | ssh
    secret_encrypted bytea NOT NULL,                     -- secretbox-sealed PAT / key
    created_at       timestamptz NOT NULL DEFAULT now(),
    created_by       uuid REFERENCES "user"(id) ON DELETE SET NULL
);

-- One credential per (workspace, host, owner): the daemon resolves a repo to a
-- single credential by its host + owner. host/owner are stored lowercased by the
-- handler so the match is case-insensitive.
CREATE UNIQUE INDEX git_credential_ws_host_owner_idx
    ON git_credential (workspace_id, host, owner);
