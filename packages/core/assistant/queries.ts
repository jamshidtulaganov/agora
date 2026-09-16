import { queryOptions, useQuery } from "@tanstack/react-query";
import { api } from "../api";

// The Agora Assistant is USER-scoped, not workspace-scoped (see
// docs/agora-assistant-plan.md §3-4): one assistant conversation spans every
// workspace the person belongs to. The root key is deliberately NOT keyed by
// wsId — unlike every other query key in this codebase — so switching
// workspaces never invalidates or refetches assistant data.
export const assistantKeys = {
  all: ["assistant"] as const,
  sessions: () => [...assistantKeys.all, "sessions"] as const,
  session: (id: string) => [...assistantKeys.all, "session", id] as const,
  messages: (sessionId: string) => [...assistantKeys.all, "messages", sessionId] as const,
  runs: (sessionId: string) => [...assistantKeys.all, "runs", sessionId] as const,
  run: (id: string) => [...assistantKeys.all, "run", id] as const,
  operations: (sessionId: string) => [...assistantKeys.all, "operations", sessionId] as const,
  operation: (id: string) => [...assistantKeys.all, "operation", id] as const,
  availability: () => [...assistantKeys.all, "availability"] as const,
  /** Prefix for every single-artifact query — lets a WS event invalidate
   *  "whatever artifact is on screen" without knowing its id. */
  artifacts: () => [...assistantKeys.all, "artifact"] as const,
  artifact: (id: string) => [...assistantKeys.artifacts(), id] as const,
  sessionArtifacts: (sessionId: string) =>
    [...assistantKeys.all, "session-artifacts", sessionId] as const,
};

export function assistantSessionListOptions() {
  return queryOptions({
    queryKey: assistantKeys.sessions(),
    queryFn: () => api.listAssistantSessions(),
    // Kept fresh by WS invalidation (assistant:message / assistant:run_finished
    // bump title/updated_at) — see ws-updaters.ts. No polling.
    staleTime: Infinity,
  });
}

export function assistantSessionOptions(id: string) {
  return queryOptions({
    queryKey: assistantKeys.session(id),
    queryFn: () => api.getAssistantSession(id),
    enabled: !!id,
    staleTime: Infinity,
  });
}

export function assistantMessagesOptions(sessionId: string) {
  return queryOptions({
    queryKey: assistantKeys.messages(sessionId),
    queryFn: () => api.listAssistantMessages(sessionId),
    enabled: !!sessionId,
    staleTime: Infinity,
  });
}

export function assistantRunsOptions(sessionId: string) {
  return queryOptions({
    queryKey: assistantKeys.runs(sessionId),
    queryFn: () => api.listAssistantRuns(sessionId),
    enabled: !!sessionId,
    staleTime: Infinity,
  });
}

export function assistantRunOptions(id: string) {
  return queryOptions({
    queryKey: assistantKeys.run(id),
    queryFn: () => api.getAssistantRun(id),
    enabled: !!id,
    staleTime: Infinity,
  });
}

export function assistantOperationOptions(id: string) {
  return queryOptions({
    queryKey: assistantKeys.operation(id),
    queryFn: () => api.getAssistantOperation(id),
    enabled: !!id,
    staleTime: Infinity,
    retry: false,
  });
}

export function assistantOperationsOptions(sessionId: string) {
  return queryOptions({
    queryKey: assistantKeys.operations(sessionId),
    queryFn: () => api.listAssistantOperations(sessionId),
    enabled: !!sessionId,
    staleTime: Infinity,
    retry: false,
  });
}

export function useAssistantOperation(id: string) {
  return useQuery(assistantOperationOptions(id));
}

/**
 * UI gate: `{enabled, model_label}`. The nav item and the page both hide
 * entirely when this reports `enabled: false` (feature off or unkeyed on
 * this instance) — mirrors the `SummarizeComments` 503 pattern.
 */
export function assistantAvailabilityOptions() {
  return queryOptions({
    queryKey: assistantKeys.availability(),
    queryFn: () => api.getAssistantAvailability(),
    // Rarely changes (an instance-config flag) and has no WS signal of its
    // own; a short staleTime lets an admin's toggle propagate without the
    // user needing a hard refresh, without polling on every focus.
    staleTime: 5 * 60 * 1000,
  });
}

/**
 * One artifact with its content, fetched lazily when the pane opens (the
 * transcript card already carries id/title/kind/version from the tool
 * result, so nothing renders behind this).
 *
 * Deliberately NOT `staleTime: Infinity` like the rest of this file: there is
 * no WS event for artifact content, and `update_artifact` bumps a version
 * behind the same id, so re-opening the pane must refetch. The transcript's
 * `assistant:message` invalidation (ws-updaters.ts) covers the live case.
 */
export function assistantArtifactOptions(id: string) {
  return queryOptions({
    queryKey: assistantKeys.artifact(id),
    queryFn: () => api.getAssistantArtifact(id),
    enabled: !!id,
    // A 404 means "the backend doesn't have this artifact" (not deployed
    // yet, or deleted with its session) — retrying can't change that, and
    // the pane has a quiet unavailable state for it.
    retry: false,
  });
}

/** Every artifact produced in a session (no content). */
export function assistantSessionArtifactListOptions(sessionId: string) {
  return queryOptions({
    queryKey: assistantKeys.sessionArtifacts(sessionId),
    queryFn: () => api.listAssistantArtifacts(sessionId),
    enabled: !!sessionId,
    retry: false,
  });
}
