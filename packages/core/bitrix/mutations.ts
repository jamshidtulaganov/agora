import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { bitrixKeys } from "./queries";
import { projectKeys } from "../projects/queries";
import { useWorkspaceId } from "../hooks";
import type { BitrixImportRequest } from "./types";

/** Bulk-import selected Bitrix groups/tasks into Agora. On settle, refresh the
 * groups/tasks caches (already-synced flags change) and the issue lists so the
 * newly imported issues appear. */
export function useImportBitrixTasks() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: BitrixImportRequest) => api.importBitrixTasks(req),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: bitrixKeys.all });
      qc.invalidateQueries({ queryKey: ["issues"] });
    },
  });
}

/** Re-sync a single Bitrix-linked project on demand. The server stamps the
 * project's bitrix_synced_at synchronously (before the 202), so refetching the
 * project surfaces the new "last synced" immediately; the synced issues stream
 * in over the websocket (which invalidates the issue lists on its own). */
export function useSyncBitrixProject(projectId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: () => api.syncBitrixProject(projectId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: projectKeys.detail(wsId, projectId) });
      qc.invalidateQueries({ queryKey: projectKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: ["issues"] });
      qc.invalidateQueries({ queryKey: bitrixKeys.all });
    },
  });
}
