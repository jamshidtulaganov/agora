import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const sprintKeys = {
  all: (wsId: string) => ["sprints", wsId] as const,
  /** Per-project sprint list — the primary surface. */
  listByProject: (wsId: string, projectId: string) =>
    [...sprintKeys.all(wsId), "list", projectId] as const,
  detail: (wsId: string, id: string) =>
    [...sprintKeys.all(wsId), "detail", id] as const,
  /** Per-sprint issue list. */
  issues: (wsId: string, id: string) =>
    [...sprintKeys.all(wsId), "issues", id] as const,
  /** An issue's current sprint, derived from per-sprint membership. */
  forIssue: (wsId: string, issueId: string) =>
    [...sprintKeys.all(wsId), "for-issue", issueId] as const,
};

export function sprintListByProjectOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: sprintKeys.listByProject(wsId, projectId),
    queryFn: () => api.listProjectSprints(projectId, { workspace_id: wsId }),
    select: (data) => data.sprints,
  });
}

export function sprintDetailOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: sprintKeys.detail(wsId, id),
    queryFn: () => api.getSprint(id),
  });
}

export function sprintIssuesOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: sprintKeys.issues(wsId, id),
    queryFn: () => api.listSprintIssues(id).then((r) => r.issues),
  });
}

/**
 * Resolves the sprint an issue currently belongs to, or null. The issue API
 * response doesn't carry `sprint_id`, and there's no per-issue sprint read,
 * so we derive membership: fetch the project's sprints, then check each
 * sprint's issue list for this issue. Projects carry a small number of
 * sprints, so the fan-out is bounded; the picker reads this once on open and
 * the assign/clear mutations invalidate it. Returns null when the issue has
 * no project (sprints are project-scoped) or belongs to no sprint.
 */
export function issueSprintOptions(
  wsId: string,
  issueId: string,
  projectId: string | null,
) {
  return queryOptions({
    queryKey: sprintKeys.forIssue(wsId, issueId),
    queryFn: async () => {
      // Sprints are project-scoped, so an issue with no project has none.
      // Otherwise fetch it directly (one call) via GET /api/issues/{id}/sprint.
      if (!projectId) return null;
      return api.getIssueSprint(issueId);
    },
    enabled: !!projectId,
  });
}
