import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { projectKeys } from "./queries";

// Per-developer standing dev servers ("preview per project → per user").
// Server state only — the list is workspace-readable, writes are self-only.
export const projectDevServerKeys = {
  list: (wsId: string, projectId: string) =>
    [...projectKeys.detail(wsId, projectId), "dev-servers"] as const,
};

export function projectDevServersOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: projectDevServerKeys.list(wsId, projectId),
    queryFn: () => api.listProjectDevServers(projectId),
  });
}

export function useSetMyProjectDevServer(wsId: string, projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (baseUrl: string) => api.setMyProjectDevServer(projectId, baseUrl),
    onSettled: () =>
      qc.invalidateQueries({ queryKey: projectDevServerKeys.list(wsId, projectId) }),
  });
}

export function useDeleteMyProjectDevServer(wsId: string, projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.deleteMyProjectDevServer(projectId),
    onSettled: () =>
      qc.invalidateQueries({ queryKey: projectDevServerKeys.list(wsId, projectId) }),
  });
}
