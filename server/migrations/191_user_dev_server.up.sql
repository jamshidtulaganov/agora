-- Per-developer standing dev servers ("preview per project → per user").
-- Each workspace member declares THEIR OWN deployed dev-server URL per
-- project (e.g. https://jamshid.sdteam.uz for sd-main) — the box they already
-- develop against over VS Code Remote. QA-preview resolution then routes an
-- issue to the ASSIGNEE developer's box (agent assignee → agent.owner_id)
-- before falling back to local_directory / the project-wide qa_smoke_url.
-- Unlike local_directory.preview_url (the dev's own machine, loopback/LAN
-- only) this is a standing PUBLIC URL, embeddable from the hosted web app
-- with no daemon reachability required.
CREATE TABLE user_dev_server (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace (id) ON DELETE CASCADE,
    project_id   uuid NOT NULL REFERENCES project (id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES "user" (id) ON DELETE CASCADE,
    base_url     text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, user_id)
);

CREATE INDEX idx_user_dev_server_project ON user_dev_server (project_id);
