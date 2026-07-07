-- Per-USER editor tokens ("Settings → VS Code account integration"). A human
-- pastes a personal access token once; the daemon injects it into that user's
-- co-code editor (code-server) environment as GH_TOKEN/GITHUB_TOKEN or
-- GITLAB_TOKEN, so gh CLI + HTTPS git in the editor terminal are authenticated
-- without a per-worktree browser sign-in. Distinct from git_credential, which
-- is WORKSPACE-scoped clone credentials for the daemon: this is the person's
-- own identity, scoped to them alone. Sealed at rest with the same
-- AGORA_GIT_SECRET_KEY secretbox as git_credential.
CREATE TABLE user_editor_token (
    user_id uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (provider IN ('github', 'gitlab')),
    token_sealed bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, provider)
);
