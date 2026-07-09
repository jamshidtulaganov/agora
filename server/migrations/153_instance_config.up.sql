-- Instance-level configuration overrides. Global (not workspace-scoped):
-- these mirror the AGORA_* / feature-flag environment variables so operators
-- can flip them from Settings → Configs without a redeploy. A row here
-- OVERRIDES the environment value; absence falls back to env, then to the
-- registry default. Secrets are never stored here — they stay in Fly secrets.
CREATE TABLE IF NOT EXISTS instance_config (
    key         TEXT PRIMARY KEY,
    value       TEXT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by  UUID
);
