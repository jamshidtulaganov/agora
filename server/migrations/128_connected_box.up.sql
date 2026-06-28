-- Remote Boxes (opt-in, additive). A connected_box is a developer's own remote
-- dev server (e.g. jamshid.sdteam.uz) that Agora onboards over SSH: it installs
-- and runs a NORMAL native self-host daemon on the box (runtime_mode stays
-- 'local'), then reaches that daemon's editor over an SSH tunnel. This table is
-- the new, parallel onboarding/transport layer — it does NOT change the
-- agent/task/runtime model. daemon_id links to the agent_runtime rows the box's
-- daemon registers (string match on agent_runtime.daemon_id; not a FK because a
-- daemon hosts many runtime rows). owner_id is the developer who owns the box,
-- mirroring agent_runtime.owner_id -> "user"(id). deploy_pubkey is the PUBLIC
-- half of the per-box keypair Agora generated; the dev authorizes it in their
-- box's authorized_keys. The private half is never stored here (encrypted at
-- rest by the control-plane; added with the bootstrapper).
CREATE TABLE IF NOT EXISTS connected_box (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    owner_id          uuid REFERENCES "user"(id) ON DELETE SET NULL,
    label             text NOT NULL,
    ssh_host          text NOT NULL,
    ssh_user          text NOT NULL,
    ssh_port          integer NOT NULL DEFAULT 22,
    deploy_pubkey     text NOT NULL DEFAULT '',
    daemon_id         uuid,
    status            text NOT NULL DEFAULT 'pending',
    last_error        text NOT NULL DEFAULT '',
    last_bootstrap_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_connected_box_workspace ON connected_box (workspace_id);
CREATE INDEX IF NOT EXISTS idx_connected_box_owner ON connected_box (workspace_id, owner_id);
CREATE INDEX IF NOT EXISTS idx_connected_box_daemon ON connected_box (daemon_id);
