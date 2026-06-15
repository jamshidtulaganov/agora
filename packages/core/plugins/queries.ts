import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import { workspaceKeys } from "../workspace/queries";

// The Plugins page has its own dedicated list endpoint (`GET /api/plugins`),
// cached under the workspace key tree so a workspace switch drops it and an
// install mutation (which also touches agents) can invalidate it cheaply.
export function pluginListOptions(wsId: string) {
  return queryOptions({
    queryKey: workspaceKeys.plugins(wsId),
    queryFn: () => api.listPlugins(),
  });
}

// Re-exported so the page can pull its supporting data (the workspace agent
// list for the install/uninstall control, and the workspace skill list for
// the create-plugin multi-select) from a single `@agora/core/plugins` entry —
// the same way the MCP page imports `agentListOptions` from `@agora/core/mcp`.
export { agentListOptions, skillListOptions } from "../workspace/queries";
