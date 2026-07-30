-- Short-lived Telegram login state must survive rolling deploys and be shared
-- by every backend instance. Keeping this in process memory makes a valid
-- deep-link look expired when /auth/telegram/start and /telegram/webhook land
-- on different processes.
CREATE TABLE telegram_login_attempt (
    nonce             text PRIMARY KEY,
    telegram_identity text NOT NULL DEFAULT '',
    first_name        text NOT NULL DEFAULT '',
    code              text NOT NULL DEFAULT '',
    attempts          integer NOT NULL DEFAULT 0,
    expires_at        timestamptz NOT NULL DEFAULT now() + interval '5 minutes',
    consumed_at       timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_telegram_login_attempt_expiry
    ON telegram_login_attempt(expires_at);
