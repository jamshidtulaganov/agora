ALTER TABLE "user"
    DROP COLUMN IF EXISTS last_active_at,
    DROP COLUMN IF EXISTS last_login_at;
