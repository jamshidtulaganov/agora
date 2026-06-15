import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { bitrixKeys } from "./queries";
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
