import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { workspaceKeys } from "../workspace/queries";
import type { CreatePluginRequest } from "./types";

// Mutations for the workspace Plugins page. Create/delete only touch the
// plugin list; install/uninstall additionally mutate each target agent's
// skills + mcp_config server-side, so they also invalidate the workspace
// agent list (`workspaceKeys.agents`) — the same list the install control
// reads to render its agent checkboxes.

/** Creates a plugin (`POST /api/plugins`) and refreshes the plugin list. */
export function useCreatePlugin(workspaceId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreatePluginRequest) => api.createPlugin(body),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workspaceKeys.plugins(workspaceId) });
    },
  });
}

/** Deletes a plugin (`DELETE /api/plugins/{id}`) and refreshes the list. */
export function useDeletePlugin(workspaceId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (pluginId: string) => api.deletePlugin(pluginId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workspaceKeys.plugins(workspaceId) });
    },
  });
}

/**
 * Installs a plugin onto agents (`POST /api/plugins/{id}/install`). Binds the
 * plugin's skills + merges its connectors into each agent's mcp_config, so we
 * invalidate both the plugin list (updates `installed_agent_ids`) and the
 * agent list (agents' skills/mcp_config changed).
 */
export function useInstallPlugin(workspaceId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { pluginId: string; agentIds: string[] }) =>
      api.installPlugin(vars.pluginId, vars.agentIds),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workspaceKeys.plugins(workspaceId) });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(workspaceId) });
    },
  });
}

/**
 * Uninstalls a plugin from agents (`POST /api/plugins/{id}/uninstall`).
 * Mirrors useInstallPlugin's invalidations — agents' skills/mcp_config and the
 * plugin's `installed_agent_ids` both change.
 */
export function useUninstallPlugin(workspaceId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { pluginId: string; agentIds: string[] }) =>
      api.uninstallPlugin(vars.pluginId, vars.agentIds),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workspaceKeys.plugins(workspaceId) });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(workspaceId) });
    },
  });
}
