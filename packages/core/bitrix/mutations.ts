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
    // Authorization/configuration failures are deterministic. Retrying a 403
    // made one click hammer the import endpoint several times while the UI hid
    // the actual error.
    retry: false,
    onSettled: () => {
      qc.invalidateQueries({ queryKey: bitrixKeys.all });
      qc.invalidateQueries({ queryKey: ["issues"] });
    },
  });
}

/** Stop the in-flight import run. Invalidates the progress query so the panel
 * immediately reflects the stop instead of waiting for the next poll tick. */
export function useCancelBitrixImport() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.cancelBitrixImport(),
    retry: false,
    onSettled: () => {
      qc.invalidateQueries({ queryKey: bitrixKeys.all });
      // Tasks imported before the stop are real issues — refresh the board.
      qc.invalidateQueries({ queryKey: ["issues"] });
    },
  });
}

/** Import tasks assigned to the authenticated user's linked Bitrix identity.
 * Unlike the selector import, this is available to every workspace member. */
export function useImportMyBitrixTasks() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.importMyBitrixTasks(),
    retry: false,
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
