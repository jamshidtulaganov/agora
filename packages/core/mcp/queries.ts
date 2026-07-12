// The MCP servers page lists every agent in the workspace and the MCP servers
// each has configured. Every agent's `mcp_config` is already inline in the
// workspace agent-list response, so there is no dedicated MCP query — we reuse
// the existing `agentListOptions` (workspace/queries.ts). Re-exported here so
// the page can import its data layer from a single `@agora/core/mcp` entry.
import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import { workspaceKeys } from "../workspace/queries";

export { agentListOptions } from "../workspace/queries";

/**
 * Workspace remote-MCP credential status list (sealed auth for remote http/sse
 * servers). Status only — has_secret + last4, never the token. The panel joins
 * this with each agent's mcp_config to show a "sealed auth" badge on a remote
 * server whose name has a stored credential.
 */
export function mcpCredentialsOptions(workspaceId: string) {
  return queryOptions({
    queryKey: workspaceKeys.mcpCredentials(workspaceId),
    queryFn: () => api.listMcpCredentials(workspaceId),
  });
}
