DROP TABLE IF EXISTS telegram_chat_session;
ALTER TABLE telegram_installation
    DROP COLUMN IF EXISTS admin_telegram_user_ids,
    DROP COLUMN IF EXISTS allowed_chat_ids;
