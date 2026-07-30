-- name: CreateTelegramLoginAttempt :exec
WITH expired AS (
    DELETE FROM telegram_login_attempt
    WHERE expires_at < now() - interval '1 hour'
    RETURNING nonce
)
INSERT INTO telegram_login_attempt (nonce)
VALUES ($1);

-- name: BindTelegramLoginAttempt :execrows
UPDATE telegram_login_attempt
SET telegram_identity = $2,
    first_name = $3,
    code = $4
WHERE nonce = $1
  AND expires_at > now()
  AND consumed_at IS NULL;

-- name: VerifyTelegramLoginAttempt :one
UPDATE telegram_login_attempt
SET attempts = CASE
        WHEN code = sqlc.arg(submitted_code) THEN attempts
        ELSE attempts + 1
    END,
    consumed_at = CASE
        WHEN code = sqlc.arg(submitted_code) THEN now()
        ELSE consumed_at
    END
WHERE nonce = sqlc.arg(nonce)
  AND telegram_identity <> ''
  AND expires_at > now()
  AND consumed_at IS NULL
  AND attempts < 5
RETURNING telegram_identity,
          first_name,
          code = sqlc.arg(submitted_code) AS valid;
