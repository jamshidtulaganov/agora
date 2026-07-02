import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { zohoDynKeys, zohoKeys } from "./queries";
import type {
  CreateZohoSyncConfigRequest,
  PutZohoConnectionRequest,
  UpdateZohoSyncConfigRequest,
  ZohoImportRequest,
  ZohoSprintsImportRequest,
} from "./types";

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

// --- Dynamic Zoho integration -----------------------------------------------
// All hooks take wsId as a parameter (CLAUDE.md rule) and invalidate the
// matching zohoDynKeys on settle so success AND failure both resync the cache.

/** Save (or rotate) the workspace Zoho connection. Discovery results depend
 * on the credentials, so modules/configs are refreshed too. */
export function useSaveZohoConnection(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: PutZohoConnectionRequest) =>
      api.putZohoConnection(wsId, req),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: zohoDynKeys.connection(wsId) });
      qc.invalidateQueries({ queryKey: zohoDynKeys.crmModules(wsId) });
      qc.invalidateQueries({ queryKey: zohoDynKeys.syncConfigs(wsId) });
    },
  });
}

/** Remove the workspace Zoho connection. Bindings and sync configs hang off
 * the connection row server-side, so every dynamic-Zoho query refreshes. */
export function useDeleteZohoConnection(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.deleteZohoConnection(wsId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: zohoDynKeys.connection(wsId) });
      qc.invalidateQueries({ queryKey: zohoDynKeys.userBinding(wsId) });
      qc.invalidateQueries({ queryKey: zohoDynKeys.crmModules(wsId) });
      qc.invalidateQueries({ queryKey: zohoDynKeys.syncConfigs(wsId) });
    },
  });
}

/** Exchange a pasted self-client grant code for the caller's own binding. */
export function useSaveZohoUserBinding(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (grantCode: string) => api.putZohoUserBinding(wsId, grantCode),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: zohoDynKeys.userBinding(wsId) });
    },
  });
}

/** Remove the caller's own Zoho binding. */
export function useDeleteZohoUserBinding(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.deleteZohoUserBinding(wsId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: zohoDynKeys.userBinding(wsId) });
    },
  });
}

/** Create a sync config for one CRM module. */
export function useCreateZohoSyncConfig(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateZohoSyncConfigRequest) =>
      api.createZohoSyncConfig(wsId, req),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: zohoDynKeys.syncConfigs(wsId) });
    },
  });
}

/** Partially update one sync config (direction / project / maps / enabled). */
export function useUpdateZohoSyncConfig(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      configId,
      req,
    }: {
      configId: string;
      req: UpdateZohoSyncConfigRequest;
    }) => api.updateZohoSyncConfig(wsId, configId, req),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: zohoDynKeys.syncConfigs(wsId) });
    },
  });
}

/** Delete one sync config. */
export function useDeleteZohoSyncConfig(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (configId: string) => api.deleteZohoSyncConfig(wsId, configId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: zohoDynKeys.syncConfigs(wsId) });
    },
  });
}
