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
