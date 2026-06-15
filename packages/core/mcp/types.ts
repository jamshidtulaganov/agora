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
    argsText: "-y figma-developer-mcp --stdio",
    envKeys: ["FIGMA_API_KEY"],
  },
  {
    id: "github",
    label: "GitHub",
    name: "github",
    command: "npx",
    argsText: "-y @modelcontextprotocol/server-github",
    envKeys: ["GITHUB_PERSONAL_ACCESS_TOKEN"],
  },
  {
    id: "mysql",
    label: "MySQL",
    name: "mysql",
    command: "mcp-server-mysql",
    argsText: "",
    envKeys: ["MYSQL_HOST", "MYSQL_DB", "MYSQL_USER", "MYSQL_PASS"],
  },
];
