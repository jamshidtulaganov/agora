-- Per-agent Telegram bots, so an agent can speak in a group under its own
-- identity and be replied to.
--
-- Mirrors lark_installation, which already proves the shape: one bot per agent,
-- token sealed at rest, workspace-scoped. The platform-wide TELEGRAM_BOT_TOKEN
-- stays what it is — login OTP, DMs, and autopilot report delivery. This table
-- is only for agents that need their own voice in a chat.
CREATE TABLE telegram_installation (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id       uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id           uuid NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    -- Sealed with AGORA_TELEGRAM_SECRET_KEY (secretbox). A bot token grants
    -- full control of the bot, including posting to every chat it is in, so it
    -- is never stored in plaintext and never returned by the API.
    bot_token_encrypted bytea NOT NULL,
    -- Cached from getMe at install time: shown in the UI and used to detect an
    -- @mention of THIS agent in a group.
    bot_username       text NOT NULL,
    bot_user_id        bigint NOT NULL,
    -- The group this agent speaks in. Nullable: an installation can exist
    -- before anyone has added the bot to a chat.
    chat_id            text,
    installer_user_id  uuid REFERENCES "user"(id) ON DELETE SET NULL,
    status             text NOT NULL DEFAULT 'active',
    installed_at       timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- One bot per agent. Re-installing replaces the row rather than accumulating
-- stale tokens that would each keep receiving updates.
CREATE UNIQUE INDEX idx_telegram_installation_agent ON telegram_installation(agent_id);
CREATE INDEX idx_telegram_installation_workspace ON telegram_installation(workspace_id);
-- Inbound routing looks up by bot identity on every update, so it must be fast
-- and unambiguous: two agents cannot share one bot.
CREATE UNIQUE INDEX idx_telegram_installation_bot_user ON telegram_installation(bot_user_id);
