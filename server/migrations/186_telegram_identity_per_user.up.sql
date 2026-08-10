-- Keep the newest Telegram mapping if an older deployment accumulated more
-- than one row for a user before replacement became transactional.
WITH ranked AS (
    SELECT ctid,
           row_number() OVER (
               PARTITION BY user_id
               ORDER BY updated_at DESC, created_at DESC, external_id DESC
           ) AS position
    FROM user_external_identity
    WHERE provider = 'telegram'
)
DELETE FROM user_external_identity identity
USING ranked
WHERE identity.ctid = ranked.ctid
  AND ranked.position > 1;

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_external_identity_telegram_user
    ON user_external_identity (user_id)
    WHERE provider = 'telegram';
