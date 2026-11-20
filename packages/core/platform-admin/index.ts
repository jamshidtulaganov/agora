import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";

export type { AdminOverview, AdminUser, AdminWorkspace, AdminGrowth, AdminFunnelRow } from "../api/schemas";

// Platform admin (/admin) data. Instance-wide, not workspace-scoped: the key
// does not include a workspace id on purpose.
export const platformAdminKeys = {
  overview: () => ["platform-admin", "overview"] as const,
  growth: (days: number) => ["platform-admin", "growth", days] as const,
};

export function adminGrowthOptions(days: number) {
  return queryOptions({
    queryKey: platformAdminKeys.growth(days),
    queryFn: () => api.getAdminGrowth(days),
    retry: false,
  });
}

export function adminOverviewOptions() {
  return queryOptions({
    queryKey: platformAdminKeys.overview(),
    queryFn: () => api.getAdminOverview(),
    retry: false,
  });
}

export type AdminPlanChange = {
  mode: "team" | "enterprise" | "free" | "trial";
  interval?: "" | "month" | "year";
  seats?: number;
  until?: string;
  trial_until?: string;
  note?: string;
};

/**
 * Not optimistic on purpose: a plan change is a money record the operator
 * must see confirmed by the server, not a UI preference.
 */
export function useSetAdminWorkspacePlan() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ workspaceId, change }: { workspaceId: string; change: AdminPlanChange }) =>
      api.setAdminWorkspaceSubscription(workspaceId, change),
    onSettled: () => qc.invalidateQueries({ queryKey: platformAdminKeys.overview() }),
  });
}
