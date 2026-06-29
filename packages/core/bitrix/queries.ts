import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

/** React-query keys for the Bitrix import browser. Workspace scoping is carried
 * by the X-Workspace-Slug header (added by the ApiClient), so the keys don't
 * need a workspace id — but switching workspaces clears the cache via the app's
 * standard query reset, so groups/tasks always reflect the active workspace. */
export const bitrixKeys = {
  all: ["bitrix"] as const,
  groups: () => [...bitrixKeys.all, "groups"] as const,
  users: () => [...bitrixKeys.all, "users"] as const,
  tasks: (groupId: string) => [...bitrixKeys.all, "tasks", groupId] as const,
};

/** Bitrix workgroups + the Agora workspace each routes to. */
export function bitrixGroupsOptions() {
  return queryOptions({
    queryKey: bitrixKeys.groups(),
    queryFn: () => api.listBitrixGroups(),
  });
}

/** Bitrix portal users, for the "import by responsible" flow. */
export function bitrixUsersOptions() {
  return queryOptions({
    queryKey: bitrixKeys.users(),
    queryFn: () => api.listBitrixUsers(),
  });
}

/** Live progress of the most recent background import run. Poll while running. */
export function bitrixImportProgressOptions(enabled: boolean) {
  return queryOptions({
    queryKey: [...bitrixKeys.all, "import-progress"] as const,
    queryFn: () => api.getBitrixImportProgress(),
    enabled,
    refetchInterval: enabled ? 1500 : false,
  });
}

/** Tasks in one Bitrix workgroup, with already-synced state. */
export function bitrixTasksOptions(groupId: string) {
  return queryOptions({
    queryKey: bitrixKeys.tasks(groupId),
    queryFn: () => api.listBitrixTasks(groupId),
    enabled: Boolean(groupId),
  });
}
