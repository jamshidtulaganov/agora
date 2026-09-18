import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

/**
 * Pinned reports — assistant artifacts published to a project
 * (docs/assistant-domain-plan.md Phase 2a).
 *
 * Unlike the assistant's own keys (user-scoped, deliberately NOT keyed on
 * wsId), a pin is workspace-scoped: it lives in one workspace and is readable
 * by that workspace's members. So these keys carry `wsId` like every other
 * workspace query — switching workspaces swaps the cache entry with no
 * manual invalidation.
 */
export const reportKeys = {
  all: (wsId: string) => ["reports", wsId] as const,
  project: (wsId: string, projectId: string) =>
    [...reportKeys.all(wsId), "project", projectId] as const,
  detail: (wsId: string, pinId: string) =>
    [...reportKeys.all(wsId), "detail", pinId] as const,
};

/**
 * Reports pinned to one project (metadata only).
 *
 * `retry: false` for the same reason the artifact reads have it: until these
 * routes are deployed this is a hard 404, and the section's job on error is
 * to render nothing, not to hammer.
 */
export function projectReportsOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: reportKeys.project(wsId, projectId),
    queryFn: () => api.listProjectReports(projectId),
    enabled: !!wsId && !!projectId,
    retry: false,
  });
}

/**
 * One pinned report with its current body, fetched only while the viewer is
 * open.
 *
 * Deliberately NOT `staleTime: Infinity`: the pin always shows the artifact's
 * LATEST content, and the owner re-running a recipe bumps that body behind
 * the same pin id. `report:updated` invalidates this key live; re-opening the
 * viewer refetches in every other case.
 */
export function reportOptions(wsId: string, pinId: string) {
  return queryOptions({
    queryKey: reportKeys.detail(wsId, pinId),
    queryFn: () => api.getReport(pinId),
    enabled: !!wsId && !!pinId,
    retry: false,
  });
}
