import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { sprintKeys } from "./queries";
import { useWorkspaceId } from "../hooks";
import type {
  Sprint,
  CreateSprintRequest,
  UpdateSprintRequest,
  ListSprintsResponse,
} from "../types";

export function useCreateSprint(projectId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (data: CreateSprintRequest) => api.createSprint(projectId, data),
    onSuccess: (newSprint) => {
      qc.setQueryData<ListSprintsResponse>(
        sprintKeys.listByProject(wsId, projectId),
        (old) =>
          old && !old.sprints.some((s) => s.id === newSprint.id)
            ? { ...old, sprints: [...old.sprints, newSprint], total: old.total + 1 }
            : old,
      );
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: sprintKeys.listByProject(wsId, projectId) });
    },
  });
}

export function useUpdateSprint(projectId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ id, ...data }: { id: string } & UpdateSprintRequest) =>
      api.updateSprint(id, data),
    onMutate: ({ id, ...data }) => {
      qc.cancelQueries({ queryKey: sprintKeys.listByProject(wsId, projectId) });
      const prevList = qc.getQueryData<ListSprintsResponse>(
        sprintKeys.listByProject(wsId, projectId),
      );
      const prevDetail = qc.getQueryData<Sprint>(sprintKeys.detail(wsId, id));
      qc.setQueryData<ListSprintsResponse>(
        sprintKeys.listByProject(wsId, projectId),
        (old) =>
          old
            ? { ...old, sprints: old.sprints.map((s) => (s.id === id ? { ...s, ...data } : s)) }
            : old,
      );
      qc.setQueryData<Sprint>(sprintKeys.detail(wsId, id), (old) =>
        old ? { ...old, ...data } : old,
      );
      return { prevList, prevDetail, id };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prevList)
        qc.setQueryData(sprintKeys.listByProject(wsId, projectId), ctx.prevList);
      if (ctx?.prevDetail)
        qc.setQueryData(sprintKeys.detail(wsId, ctx.id), ctx.prevDetail);
    },
    onSettled: (_data, _err, vars) => {
      qc.invalidateQueries({ queryKey: sprintKeys.detail(wsId, vars.id) });
      qc.invalidateQueries({ queryKey: sprintKeys.listByProject(wsId, projectId) });
    },
  });
}

export function useDeleteSprint(projectId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (id: string) => api.deleteSprint(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: sprintKeys.listByProject(wsId, projectId) });
      const prevList = qc.getQueryData<ListSprintsResponse>(
        sprintKeys.listByProject(wsId, projectId),
      );
      qc.setQueryData<ListSprintsResponse>(
        sprintKeys.listByProject(wsId, projectId),
        (old) =>
          old
            ? { ...old, sprints: old.sprints.filter((s) => s.id !== id), total: old.total - 1 }
            : old,
      );
      qc.removeQueries({ queryKey: sprintKeys.detail(wsId, id) });
      return { prevList };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.prevList)
        qc.setQueryData(sprintKeys.listByProject(wsId, projectId), ctx.prevList);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: sprintKeys.listByProject(wsId, projectId) });
    },
  });
}

/**
 * Assign / clear an issue's sprint. Mirrors useAttachLabel/useDetachLabel —
 * keyed by issueId, mutates the issue↔sprint link, and invalidates the
 * derived `forIssue` query plus the affected sprints' issue lists so both
 * the picker and any open sprint view refresh. `sprintId === null` clears.
 */
export function useSetIssueSprint(issueId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    // Normalize both branches to void — the assigned sprint comes back via
    // the `forIssue` invalidation below, so callers don't consume the result.
    mutationFn: async (sprintId: string | null): Promise<void> => {
      if (sprintId === null) {
        await api.clearIssueSprint(issueId);
      } else {
        await api.assignIssueSprint(issueId, sprintId);
      }
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: sprintKeys.forIssue(wsId, issueId) });
      // The issue moved into/out of one or more sprints — refresh every
      // sprint issue list in the workspace (bounded; sprint lists are small).
      qc.invalidateQueries({ queryKey: sprintKeys.all(wsId) });
    },
  });
}
