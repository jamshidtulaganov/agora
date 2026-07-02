// Types + pure helpers for the workspace-level MCP servers admin page.
//
// The canonical on-agent shape is `agent.mcp_config` (see Agent.mcp_config in
// types/agent.ts), a JSON object with a top-level `mcpServers` map:
//
//   { "mcpServers": { "<name>": { "command": "...", "args": [...], "env": {...} } } }
//
// The daemon materialises this per-runtime (Claude flags, Codex config.toml,
// etc.). We only ever read the whole object, set/delete one key under
// `mcpServers`, and write the whole object back — preserving every other
// server the agent already has.

/** One MCP server entry as stored under `mcpServers[<name>]`. */
export interface McpServerEntry {
  command: string;
  args?: string[];
  env?: Record<string, string>;
}

/** The shape of a valid `agent.mcp_config`. */
export interface McpConfig {
  mcpServers: Record<string, McpServerEntry>;
}

/**
 * A server entry paired with its name, for list rendering. `command` is
 * optional here (unlike McpServerEntry) because the source is parsed from an
 * untrusted `mcp_config` JSON blob where the field may be missing — the UI
 * renders it defensively.
 */
export interface NamedMcpServer extends Partial<McpServerEntry> {
  name: string;
}

/**
 * Narrowing read of an unknown `mcp_config`. Returns the `mcpServers` map when
 * the value is a well-formed config object, otherwise an empty map. Mirrors the
 * rule in agents/components/tabs/mcp-config-tab.tsx: a config must be a plain
 * object with a top-level `mcpServers` key — anything else (null, array,
 * primitive, missing key) is treated as "no servers".
 */
export function readMcpServers(config: unknown): Record<string, McpServerEntry> {
  if (config === null || typeof config !== "object" || Array.isArray(config)) {
    return {};
  }
  const servers = (config as { mcpServers?: unknown }).mcpServers;
  if (servers === null || typeof servers !== "object" || Array.isArray(servers)) {
    return {};
  }
  return servers as Record<string, McpServerEntry>;
}

/** Flattens an `mcp_config` into a sorted `NamedMcpServer[]` for display. */
export function listMcpServers(config: unknown): NamedMcpServer[] {
  const servers = readMcpServers(config);
  return Object.keys(servers)
    .sort((a, b) => a.localeCompare(b))
    .map((name) => ({ name, ...servers[name] }));
}

/**
 * Returns a new `mcp_config` with `entry` set under `mcpServers[name]`,
 * preserving every existing server. Reads the current servers off whatever
 * shape `current` is (including the no-config case) and writes back a clean
 * `{ mcpServers }` object.
 */
export function upsertMcpServer(
  current: unknown,
  name: string,
  entry: McpServerEntry,
): McpConfig {
  const servers = { ...readMcpServers(current) };
  servers[name] = entry;
  return { mcpServers: servers };
}

/**
 * Returns a new `mcp_config` with `mcpServers[name]` removed, preserving the
 * rest. When the last server is removed the result is `{ mcpServers: {} }`
 * rather than `null` so the column stays a valid (empty) config; callers that
 * want to clear the column entirely should pass `null` to updateAgent directly.
 */
export function removeMcpServer(current: unknown, name: string): McpConfig {
  const servers = { ...readMcpServers(current) };
  delete servers[name];
  return { mcpServers: servers };
}

/**
 * Builds an `McpServerEntry` from raw form fields. `argsText` is split on
 * whitespace (the form takes args as one space-separated string); empty env
 * keys are dropped. `args`/`env` are omitted entirely when empty so the stored
 * JSON stays minimal.
 */
export function buildServerEntry(input: {
  command: string;
  argsText: string;
  env: Array<{ key: string; value: string }>;
}): McpServerEntry {
  const entry: McpServerEntry = { command: input.command.trim() };
  const args = input.argsText
    .trim()
    .split(/\s+/)
    .filter((a) => a.length > 0);
  if (args.length > 0) entry.args = args;
  const env: Record<string, string> = {};
  for (const { key, value } of input.env) {
    const k = key.trim();
    if (k) env[k] = value;
  }
  if (Object.keys(env).length > 0) entry.env = env;
  return entry;
}

/** A quick-template the Add-server form can pre-fill from. */
export interface McpServerTemplate {
  id: string;
  label: string;
  /** Suggested server name (used as the `mcpServers` key). */
  name: string;
  command: string;
  argsText: string;
  /** Env keys the user is expected to fill; values start empty. */
  envKeys: string[];
  /**
   * For source-control servers (github/gitlab): the token permissions the
   * operator must grant so agents can create branches and open PRs/MRs. Shown
   * next to the template so the right scopes are requested at setup time.
   */
  scopeHint?: string;
}

/**
 * Quick templates surfaced as buttons above the Add-server form. Values for
 * env keys are intentionally blank — the operator pastes their own secrets.
 */
export const MCP_SERVER_TEMPLATES: McpServerTemplate[] = [
  {
    id: "figma",
    label: "Figma",
    name: "figma",
    command: "npx",
    // Pinned — keep in sync with figmaMcpVersion in
    // server/internal/handler/figma_mcp.go and the Dockerfile.daemon
    // preinstall. The backend auto-provisions this exact entry at claim time
    // when an issue references Figma and the agent has none configured.
    argsText: "-y figma-developer-mcp@0.13.2 --stdio --no-telemetry",
    envKeys: ["FIGMA_API_KEY"],
    scopeHint:
      "PAT from a Dev/Full seat with File content (read) scope. View/Collab-seat tokens are rate-limited to ~6 requests/month and will not work. Leave the key blank to use the workspace credential from Settings → Integrations → Figma.",
  },
  {
    id: "github",
    label: "GitHub",
    name: "github",
    command: "npx",
    argsText: "-y @modelcontextprotocol/server-github",
    envKeys: ["GITHUB_PERSONAL_ACCESS_TOKEN"],
    scopeHint:
      "Token needs Contents (read & write) + Pull requests (read & write) — fine-grained PAT, or the classic `repo` scope. Required to create branches and open PRs.",
  },
  {
    id: "gitlab",
    label: "GitLab",
    name: "gitlab",
    command: "npx",
    argsText: "-y @modelcontextprotocol/server-gitlab",
    // GITLAB_API_URL is optional (self-hosted GitLab); leave blank for gitlab.com.
    envKeys: ["GITLAB_PERSONAL_ACCESS_TOKEN", "GITLAB_API_URL"],
    scopeHint:
      "Token needs the `api` scope (or `read_repository` + `write_repository`). Required to create branches and open merge requests.",
  },
  {
    id: "mysql",
    label: "MySQL",
    name: "mysql",
    command: "mcp-server-mysql",
    argsText: "",
    envKeys: ["MYSQL_HOST", "MYSQL_DB", "MYSQL_USER", "MYSQL_PASS"],
  },
  {
    id: "lark",
    label: "Feishu / Lark",
    name: "lark",
    // -t preset.im.default scopes the agent to the IM toolset: group (chat)
    // create/list, member management, and sending messages. Override via the
    // LARK_TOOLS env (comma-separated tool ids or another preset, e.g.
    // preset.doc.default / preset.calendar.default) to widen or narrow it.
    command: "npx",
    argsText: "-y @larksuiteoapi/lark-mcp mcp -t preset.im.default",
    // APP_ID/APP_SECRET are the Bot app credentials; LARK_DOMAIN selects the
    // cloud (https://open.feishu.cn mainland vs https://open.larksuite.com
    // international). When an agent has a Lark Bot bound (scan-to-install),
    // the daemon auto-injects these from the bound installation, so the
    // operator can leave them blank — they only fill them for a standalone
    // Lark app not tied to a bound Bot.
    envKeys: ["APP_ID", "APP_SECRET", "LARK_DOMAIN", "LARK_TOKEN_MODE"],
    scopeHint:
      "Uses your Lark Bot app's credentials. For group management grant the Bot the im:chat and im:message scopes (create/manage groups, add members, send messages). Add docx / calendar scopes only if you widen LARK_TOOLS.",
  },
];
