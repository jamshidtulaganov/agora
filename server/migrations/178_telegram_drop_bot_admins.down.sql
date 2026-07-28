ALTER TABLE telegram_installation
    ADD COLUMN admin_telegram_user_ids bigint[] NOT NULL DEFAULT '{}';
