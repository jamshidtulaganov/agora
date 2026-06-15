import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { workspaceKeys } from "../workspace/queries";

/**
 * Persist an agent's MCP config from the workspace MCP-servers page. There is
 * no pre-existing react-query hook for updating an agent (only the raw
 * `api.updateAgent` ApiClient method), so this is the thin wrapper.
 *
 * The caller is responsible for merging the new/edited server into the agent's
 * existing `mcp_config` (see upsertMcpServer / removeMcpServer in ./types) and
 * passing the whole object as `mcp_config`. On settle we invalidate the
 * workspace agent list so the page re-renders with the persisted config.
 */
export function useUpdateAgentMcpConfig(workspaceId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { agentId: string; mcp_config: unknown | null }) =>
      api.updateAgent(vars.agentId, { mcp_config: vars.mcp_config }),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(workspaceId) });
    },
  });
}
