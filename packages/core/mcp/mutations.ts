import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { McpCredentialInput } from "../types";
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

/**
 * Seal (or rotate) the auth for one remote MCP server, keyed by server name.
 * The secret is write-only; the server seals it and returns status only. On
 * settle we refresh the credential status list so the "sealed auth" badge
 * appears.
 */
export function usePutMcpCredential(workspaceId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { serverName: string; data: McpCredentialInput }) =>
      api.putMcpCredential(workspaceId, vars.serverName, vars.data),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workspaceKeys.mcpCredentials(workspaceId) });
    },
  });
}

/** Remove the sealed auth for one remote MCP server (by server name). */
export function useDeleteMcpCredential(workspaceId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (serverName: string) => api.deleteMcpCredential(workspaceId, serverName),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workspaceKeys.mcpCredentials(workspaceId) });
    },
  });
}


/** Save or clear the workspace-wide mcp.json document. */
export function useUpdateWorkspaceMcpConfig(workspaceId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (mcpConfig: unknown | null) =>
      api.updateWorkspaceMcpConfig(workspaceId, mcpConfig),
    onSuccess: (response) => {
      qc.setQueryData(workspaceKeys.mcpConfig(workspaceId), response);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workspaceKeys.mcpConfig(workspaceId) });
    },
  });
}
