import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

// The Release page's shared query definitions. The Queue tab, the Ship tab
// and the always-on health strip all read the SAME cache entries — a single
// factory per query guarantees one queryFn/staleTime per key, so an edit in
// one consumer can never silently repopulate another consumer's cache with
// different params (TanStack Query maps a key to exactly one queryFn).
//
// `project` uses the page-level selector's "all" sentinel so keys stay
// identical across consumers; broad invalidations may keep using the
// ["qa-cockpit", wsId] / ["qa-sprint-readiness"] prefixes.

export const qaReleaseKeys = {
  queue: (wsId: string, project: string) => ["qa-cockpit", wsId, project] as const,
  verdicts: (wsId: string, project: string) => ["qa-verdicts", wsId, project] as const,
  sprintReadiness: (wsId: string, project: string) => ["qa-sprint-readiness", wsId, project] as const,
};

// The review queue: every in_review issue in scope (workspace-wide or one
// project). limit 200 = the whole practical queue in one page.
export function qaQueueOptions(wsId: string, projectId?: string) {
  return queryOptions({
    queryKey: qaReleaseKeys.queue(wsId, projectId ?? "all"),
    queryFn: () =>
      api.listIssues({ status: "in_review", limit: 200, ...(projectId ? { project_id: projectId } : {}) }),
    staleTime: 15_000,
  });
}

// Freshest QA verdict per issue (reason + provenance + reconciled_state) —
// one batch call for the whole queue.
export function qaVerdictsOptions(wsId: string, projectId?: string) {
  return queryOptions({
    queryKey: qaReleaseKeys.verdicts(wsId, projectId ?? "all"),
    queryFn: () => api.listQAVerdicts(projectId),
    staleTime: 15_000,
  });
}

// Sprint QA-readiness rollup ("is this sprint mergeable?"). wsId in the key:
// the fetch scopes by the ambient workspace header, so without it a
// workspace switch served the previous workspace's cache.
export function sprintReadinessOptions(wsId: string, projectId?: string) {
  return queryOptions({
    queryKey: qaReleaseKeys.sprintReadiness(wsId, projectId ?? "all"),
    queryFn: () => api.getSprintReadiness(projectId),
    staleTime: 30_000,
    refetchInterval: 60_000,
  });
}
