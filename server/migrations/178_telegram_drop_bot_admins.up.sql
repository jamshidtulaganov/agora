-- Authority for /allow and /deny moves to Agora workspace roles.
--
-- A separate admin_telegram_user_ids list was a second, parallel source of
-- truth for "who administers this agent": revoking someone's Agora admin role
-- would leave their Telegram power intact, silently. There is one answer to
-- that question and it already lives in member.role.
ALTER TABLE telegram_installation DROP COLUMN IF EXISTS admin_telegram_user_ids;
