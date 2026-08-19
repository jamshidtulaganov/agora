import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

// All automation reads are workspace-scoped: the key carries wsId so switching
// workspace swaps the cache instead of showing another team's flows (CLAUDE.md
// "Workspace-scoped queries must key on wsId").

export const automationKeys = {
  all: (wsId: string) => ["automations", wsId] as const,
  list: (wsId: string) => [...automationKeys.all(wsId), "list"] as const,
  detail: (wsId: string, id: string) => [...automationKeys.all(wsId), "detail", id] as const,
  runs: (wsId: string, id: string) => [...automationKeys.all(wsId), "runs", id] as const,
  catalog: (wsId: string) => [...automationKeys.all(wsId), "catalog"] as const,
  recipes: (wsId: string) => [...automationKeys.all(wsId), "recipes"] as const,
};

export function automationListOptions(wsId: string) {
  return queryOptions({
    queryKey: automationKeys.list(wsId),
    queryFn: () => api.listAutomations(),
    select: (data) => data.automations,
  });
}

export function automationDetailOptions(wsId: string, id: string, options?: { enabled?: boolean }) {
  return queryOptions({
    queryKey: automationKeys.detail(wsId, id),
    queryFn: () => api.getAutomation(id),
    enabled: (options?.enabled ?? true) && id !== "",
  });
}

export function automationRunsOptions(wsId: string, id: string, options?: { enabled?: boolean }) {
  return queryOptions({
    queryKey: automationKeys.runs(wsId, id),
    queryFn: () => api.listAutomationRuns(id),
    select: (data) => data.runs,
    enabled: (options?.enabled ?? true) && id !== "",
  });
}

// The node palette. It changes only on a server deploy, so it is cached for the
// session rather than refetched per editor mount.
export function automationCatalogOptions(wsId: string) {
  return queryOptions({
    queryKey: automationKeys.catalog(wsId),
    queryFn: () => api.getAutomationCatalog(),
    staleTime: 30 * 60 * 1000,
  });
}

export function automationRecipesOptions(wsId: string) {
  return queryOptions({
    queryKey: automationKeys.recipes(wsId),
    queryFn: () => api.listAutomationRecipes(),
    select: (data) => data.recipes,
  });
}
