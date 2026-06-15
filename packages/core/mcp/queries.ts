// The MCP servers page lists every agent in the workspace and the MCP servers
// each has configured. Every agent's `mcp_config` is already inline in the
// workspace agent-list response, so there is no dedicated MCP query — we reuse
// the existing `agentListOptions` (workspace/queries.ts). Re-exported here so
// the page can import its data layer from a single `@agora/core/mcp` entry.
export { agentListOptions } from "../workspace/queries";
