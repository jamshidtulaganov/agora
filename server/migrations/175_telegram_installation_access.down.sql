ALTER TABLE telegram_installation
    DROP CONSTRAINT IF EXISTS telegram_installation_access_policy_check,
    DROP COLUMN IF EXISTS allowed_telegram_user_ids,
    DROP COLUMN IF EXISTS access_policy;
