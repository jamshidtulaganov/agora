import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { zohoKeys } from "./queries";
import type { ZohoImportRequest, ZohoSprintsImportRequest } from "./types";

/** Bulk-import selected Zoho Projects projects into Agora. On settle, refresh the
 * project picker (imported state) and the issue lists so the newly imported
 * issues appear. Mirrors useImportBitrixTasks. */
export function useImportZohoProjects() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: ZohoImportRequest) => api.importZohoProjects(req),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: zohoKeys.all });
      qc.invalidateQueries({ queryKey: ["issues"] });
    },
  });
}

/** Bulk-import selected Zoho Sprints projects into Agora. */
export function useImportZohoSprintsProjects() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: ZohoSprintsImportRequest) =>
      api.importZohoSprintsProjects(req),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: zohoKeys.all });
      qc.invalidateQueries({ queryKey: ["issues"] });
    },
  });
}
