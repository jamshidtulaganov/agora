-- When a person last signed in, and when they last used the app. Read only by
-- the platform admin page (/admin). last_active_at is stamped by the auth
-- middleware at most once per few minutes per user, so it costs one small
-- UPDATE per active user per window, not one per request.
ALTER TABLE "user"
    ADD COLUMN last_login_at  timestamptz,
    ADD COLUMN last_active_at timestamptz;
