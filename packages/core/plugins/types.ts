// Types for the workspace-level Plugins admin page.
//
// A plugin bundles workspace skills + MCP connectors and installs them onto
// agents as a unit (like the Claude plugin model: a plugin → multiple skills +
// connectors). Skills are referenced by id; connectors live in the same
// `mcp_config` shape the agents already use:
//
//   { "mcpServers": { "<name>": { "command": "...", "args": [...], "env": {...} } } }
//
// On install the backend binds the plugin's skills to each target agent and
// merges its connectors into the agent's own `mcp_config`, preserving every
// existing skill/server the agent already has. The list endpoint redacts
// `mcp_config` env values to "***" so secrets never leave the server.

/** Minimal skill reference embedded in a Plugin payload (`GET /api/plugins`). */
export interface PluginSkillRef {
  id: string;
  name: string;
}

/**
 * A workspace plugin as returned by `GET /api/plugins`. `mcp_config` carries
 * the bundled connectors in the canonical `{ mcpServers }` shape (or `null`
 * when the plugin ships skills only); env values inside it are redacted to
 * "***". `installed_agent_ids` lists the agents the plugin is currently
 * installed on, driving the per-plugin install/uninstall control.
 */
export interface Plugin {
  id: string;
  name: string;
  description: string;
  /** Bundled connectors: `{ mcpServers: {...} }` or `null` for skills-only. */
  mcp_config: unknown | null;
  skills: PluginSkillRef[];
  installed_agent_ids: string[];
}

/**
 * Body for `POST /api/plugins`. `mcp_config` is the connectors bundle in the
 * `{ mcpServers }` shape (or `null` for a skills-only plugin); `skill_ids`
 * is the set of workspace skills the plugin installs.
 */
export interface CreatePluginRequest {
  name: string;
  description?: string;
  mcp_config: unknown | null;
  skill_ids: string[];
}
