/** A Telegram bot installation bound to a single Agora agent.
 *
 * Wire shape mirrors `TelegramInstallationResponse` in
 * `server/internal/handler/telegram_installation.go`. It deliberately carries
 * NO token field: the bot token is full control of that bot — it can post to
 * every chat the bot is in and read everything addressed to it — so it never
 * leaves the server, not even masked.
 *
 * New fields the backend adds MUST default to optional so an older desktop
 * build keeps parsing a newer response — see CLAUDE.md → API Response
 * Compatibility. */
export interface TelegramInstallation {
  agent_id: string;
  bot_username: string;
  bot_user_id: string;
  /** Where unsolicited output goes (autopilot reports). Distinct from
   * `allowed_chat_ids`, which is who may INSTRUCT the agent: a report needs
   * one destination, whereas questions can arrive from several rooms. */
  chat_id?: string;
  status: "active" | "revoked" | string;
  installed_at?: string;
  /** Who may instruct this agent through the bot. Reporting is unaffected —
   * a `closed` bot still speaks, it just takes no orders. */
  access_policy: "closed" | "allowlist" | "open" | string;
  /** Numeric Telegram ids, carried as strings so a JS client cannot lose
   * precision: chat ids are past 2^53 and would silently round as numbers. */
  allowed_user_ids?: string[];
  allowed_chat_ids?: string[];
}

export interface ListTelegramInstallationsResponse {
  installations: TelegramInstallation[];
  /** Whether the deployment has AGORA_TELEGRAM_SECRET_KEY set. When false no
   * install can succeed, so the UI must not offer the form — failing after
   * someone has pasted a live bot token is the worst moment to find out. */
  configured: boolean;
}

/** The deep link that binds a group by QR. Adding a bot to a group cannot
 * authorize that group by itself — anyone can invite a bot anywhere — so the
 * link carries a single-use token minted by an owner/admin. */
export interface TelegramBindLinkResponse {
  group_url: string;
  bot_username: string;
  expires_at: string;
}

export interface SetTelegramAccessRequest {
  policy: "closed" | "allowlist" | "open";
  allowed_user_ids: string[];
  /** Omitted leaves the current set alone, so editing the user list cannot
   * accidentally unbind every group. */
  allowed_chat_ids?: string[];
}
