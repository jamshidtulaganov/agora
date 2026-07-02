-- Per-workspace Figma credential: one PAT that lets agents read Figma designs
-- referenced by issues (via the figma-developer-mcp server injected at claim
-- time). Sealed at rest with a secretbox loaded from AGORA_FIGMA_SECRET_KEY;
-- the plaintext token is only ever decrypted server-side into the per-task
-- mcp_config env, never returned by any endpoint.
--
-- Unlike git_credential this table carries lifecycle columns: Figma PATs
-- hard-cap at 90 days (since 2025-04-28), and a View/Collab-seat token gets
-- ~6 Tier-1 requests/MONTH — both must be surfaced at save time and re-probed,
-- or the feature silently dies a quarter later.
CREATE TABLE figma_credential (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id       uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    label              text NOT NULL DEFAULT '',
    token_encrypted    bytea NOT NULL,                    -- secretbox-sealed PAT
    token_last4        text NOT NULL DEFAULT '',
    token_kind         text NOT NULL DEFAULT 'pat',       -- 'pat' | 'plan_access_token'
    expires_at         timestamptz,                       -- PAT cap: 90d; PlanAccessToken: 365d
    seat_probe         text NOT NULL DEFAULT 'unknown',   -- 'ok' | 'low_seat' | 'unknown'
    probe_status       text NOT NULL DEFAULT '',          -- 'ok' | 'invalid' | 'expired' | 'unreachable'
    probed_at          timestamptz,
    expiry_notified_at timestamptz,                       -- dedup for the <14d expiring warning
    created_by         uuid REFERENCES "user"(id) ON DELETE SET NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id)
);
