import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export type { WorkspaceSubscription, SubscriptionPlan } from "../api/schemas";

// The workspace's plan, trial and usage. Workspace-scoped (unlike the cloud
// wallet keys in ./queries), so it keys on wsId and follows workspace switches.
export const subscriptionKeys = {
  all: (wsId: string) => ["workspace-subscription", wsId] as const,
};

export function workspaceSubscriptionOptions(wsId: string) {
  return queryOptions({
    queryKey: subscriptionKeys.all(wsId),
    queryFn: () => api.getWorkspaceSubscription(wsId),
    enabled: !!wsId,
    // A trial counts days, not seconds; one read a few minutes is plenty.
    staleTime: 5 * 60 * 1000,
  });
}

/** -1 means unlimited. */
export function isUnlimited(limit: number): boolean {
  return limit < 0;
}
