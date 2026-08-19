import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { automationKeys } from "./queries";
import type { Automation, AutomationWriteRequest } from "./types";

// Automation writes are NOT optimistic. The flow list shows a rule's authored
// state and its run counters, and the server can reject a flow the editor thought
// was valid (an unknown step, a project outside the workspace) — showing the
// accepted rule only after the server confirms keeps the canvas honest about what
// will actually run.

export function useCreateAutomation(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: AutomationWriteRequest) => api.createAutomation(data),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: automationKeys.list(wsId) });
      void queryClient.invalidateQueries({ queryKey: automationKeys.recipes(wsId) });
    },
  });
}

export function useUpdateAutomation(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: AutomationWriteRequest }) =>
      api.updateAutomation(id, data),
    onSuccess: (automation: Automation) => {
      void queryClient.invalidateQueries({ queryKey: automationKeys.list(wsId) });
      void queryClient.invalidateQueries({ queryKey: automationKeys.detail(wsId, automation.id) });
    },
  });
}

// The list toggle IS optimistic: it flips one boolean, the failure path is a
// re-fetch, and waiting on a round-trip to see a switch move reads as broken.
export function useSetAutomationEnabled(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      api.setAutomationEnabled(id, enabled),
    onMutate: async ({ id, enabled }) => {
      const key = automationKeys.list(wsId);
      await queryClient.cancelQueries({ queryKey: key });
      const previous = queryClient.getQueryData(key);
      queryClient.setQueryData(key, (old: { automations: Automation[]; total: number } | undefined) => {
        if (!old) return old;
        return {
          ...old,
          automations: old.automations.map((a) => (a.id === id ? { ...a, enabled } : a)),
        };
      });
      return { previous, key };
    },
    onError: (_error, _vars, context) => {
      if (context?.previous !== undefined) {
        queryClient.setQueryData(context.key, context.previous);
      }
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: automationKeys.list(wsId) });
    },
  });
}

export function useDeleteAutomation(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteAutomation(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: automationKeys.list(wsId) });
      void queryClient.invalidateQueries({ queryKey: automationKeys.recipes(wsId) });
    },
  });
}

// Recipes install DISABLED by default (the server's own default), so a one-click
// install can never start moving a live board before a human reads the flow.
export function useInstallAutomationRecipe(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ key, projectId, enabled }: { key: string; projectId?: string | null; enabled?: boolean }) =>
      api.installAutomationRecipe(key, { project_id: projectId ?? null, enabled: enabled ?? false }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: automationKeys.list(wsId) });
      void queryClient.invalidateQueries({ queryKey: automationKeys.recipes(wsId) });
    },
  });
}
