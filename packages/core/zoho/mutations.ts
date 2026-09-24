import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { zohoAccountKeys, zohoDynKeys, zohoKeys } from "./queries";
import { EMPTY_ZOHO_ACCOUNT } from "./types";
import type {
  CreateZohoSyncConfigRequest,
  PutZohoConnectionRequest,
  UpdateZohoSyncConfigRequest,
  ZohoAccount,
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

/** Remove the workspace Zoho connection. Sync configs hang off the
 * connection row server-side, so every dynamic-Zoho query refreshes. */
export function useDeleteZohoConnection(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.deleteZohoConnection(wsId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: zohoDynKeys.connection(wsId) });
      qc.invalidateQueries({ queryKey: zohoDynKeys.crmModules(wsId) });
      qc.invalidateQueries({ queryKey: zohoDynKeys.syncConfigs(wsId) });
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

// --- Personal Zoho account ----------------------------------------------------

/** Ask the server for the Zoho sign-in page. The caller opens the returned
 * url in the system browser; the account query refetches when the person
 * comes back, so nothing is invalidated here (connecting has not happened
 * yet when this settles). */
export function useConnectZohoAccount() {
  return useMutation({
    mutationFn: () => api.connectZohoAccount(),
  });
}

/** Disconnect the caller's Zoho account (the server also revokes it at Zoho).
 * Optimistic: the card flips to "not connected" at once and rolls back if
 * the request fails. */
export function useDisconnectZohoAccount() {
  const qc = useQueryClient();
  const key = zohoAccountKeys.mine();
  return useMutation({
    mutationFn: () => api.disconnectZohoAccount(),
    onMutate: async () => {
      await qc.cancelQueries({ queryKey: key });
      const previous = qc.getQueryData<ZohoAccount>(key);
      qc.setQueryData<ZohoAccount>(key, {
        ...EMPTY_ZOHO_ACCOUNT,
        available: previous?.available === true,
      });
      return { previous };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.previous) qc.setQueryData(key, ctx.previous);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: key });
    },
  });
}
