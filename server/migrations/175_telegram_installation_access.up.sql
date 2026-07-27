-- Who may address an agent through its bot.
--
-- Default 'closed', including for rows that already exist: an agent reachable
-- from a group chat can be instructed by anyone in that group, and these agents
-- hold repo, git, QA and deploy tooling. Opening that is a decision an operator
-- makes deliberately, never something a migration does for them.
--
-- 'closed'    — nobody. The bot still SPEAKS (reports, replies); it just takes
--               no instructions. Safe default for an installation that exists
--               only to deliver reports.
-- 'allowlist' — only the listed Telegram user ids, and only in the bound chat.
-- 'open'      — anyone, but still only in the bound chat.
ALTER TABLE telegram_installation
    ADD COLUMN access_policy text NOT NULL DEFAULT 'closed',
    ADD COLUMN allowed_telegram_user_ids bigint[] NOT NULL DEFAULT '{}';

ALTER TABLE telegram_installation
    ADD CONSTRAINT telegram_installation_access_policy_check
    CHECK (access_policy IN ('closed', 'allowlist', 'open'));
