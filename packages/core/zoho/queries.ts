import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

/** React-query keys for the Zoho import browser. Workspace scoping is carried by
 * the X-Workspace-Slug header (added by the ApiClient); switching workspaces
 * clears the cache via the app's standard query reset. Mirrors bitrixKeys. */
export const zohoKeys = {
  all: ["zoho"] as const,
  projects: () => [...zohoKeys.all, "projects"] as const,
  sprintsProjects: () => [...zohoKeys.all, "sprints-projects"] as const,
};

/** Zoho Projects projects in the configured portal, for the import picker. */
export function zohoProjectsOptions() {
  return queryOptions({
    queryKey: zohoKeys.projects(),
    queryFn: () => api.listZohoProjects(),
  });
}

/** Zoho Sprints projects in the configured team, for the import picker. */
export function zohoSprintsProjectsOptions() {
  return queryOptions({
    queryKey: zohoKeys.sprintsProjects(),
    queryFn: () => api.listZohoSprintsProjects(),
  });
}

// --- Dynamic Zoho integration -----------------------------------------------
// Unlike the import-browser keys above (header-scoped), these are explicitly
// keyed on wsId so workspace switching swaps the cache automatically
// (CLAUDE.md: workspace-scoped queries must key on wsId). All factories take
// wsId as a parameter so they work outside WorkspaceIdProvider.

export const zohoDynKeys = {
  connection: (wsId: string) => ["zoho-connection", wsId] as const,
  userBinding: (wsId: string) => ["zoho-user-binding", wsId] as const,
  crmModules: (wsId: string) => ["zoho-crm-modules", wsId] as const,
  crmFields: (wsId: string, module: string) =>
    ["zoho-crm-fields", wsId, module] as const,
  syncConfigs: (wsId: string) => ["zoho-sync-configs", wsId] as const,
};

/** Workspace Zoho connection status (member-visible, no secrets). */
export function zohoConnectionOptions(wsId: string) {
  return queryOptions({
    queryKey: zohoDynKeys.connection(wsId),
    queryFn: () => api.getZohoConnection(wsId),
  });
}

/** The caller's own Zoho account binding. */
export function zohoUserBindingOptions(wsId: string) {
  return queryOptions({
    queryKey: zohoDynKeys.userBinding(wsId),
    queryFn: () => api.getZohoUserBinding(wsId),
  });
}

/** Discovered CRM modules (owner/admin only — gate with `enabled`). */
export function zohoCRMModulesOptions(wsId: string) {
  return queryOptions({
    queryKey: zohoDynKeys.crmModules(wsId),
    queryFn: () => api.listZohoCRMModules(wsId),
  });
}

/** Fields of one CRM module, for map editors (owner/admin only). */
export function zohoCRMFieldsOptions(wsId: string, module: string) {
  return queryOptions({
    queryKey: zohoDynKeys.crmFields(wsId, module),
    queryFn: () => api.listZohoCRMFields(wsId, module),
  });
}

/** Per-module sync configs (owner/admin only — gate with `enabled`). */
export function zohoSyncConfigsOptions(wsId: string) {
  return queryOptions({
    queryKey: zohoDynKeys.syncConfigs(wsId),
    queryFn: () => api.listZohoSyncConfigs(wsId),
  });
}
