import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import { isTerminalImportStatus } from "./types";

/**
 * Query keys for the importer. Every one is keyed on wsId (CLAUDE.md: a
 * workspace-scoped query must key on the workspace, so switching workspaces
 * swaps the cache instead of showing the last one's connections), and every
 * factory takes wsId as a parameter so it works outside WorkspaceIdProvider.
 */
export const importKeys = {
  all: ["imports"] as const,
  connections: (wsId: string) => [...importKeys.all, "connections", wsId] as const,
  job: (wsId: string, jobId: string) => [...importKeys.all, "job", wsId, jobId] as const,
};

/** Stored source connections for a workspace. Metadata only — no keys. */
export function importConnectionsOptions(wsId: string) {
  return queryOptions({
    queryKey: importKeys.connections(wsId),
    queryFn: () => api.listImportConnections(wsId),
  });
}

/**
 * One import job, polled while it is live.
 *
 * The refetch interval stops at a TERMINAL status and nowhere else: an unknown
 * status keeps polling, because a value this build does not recognise is not
 * evidence that the job finished. The poll is 2s, which matches the server's
 * own progress throttle — asking faster cannot produce a newer number.
 */
export function importJobOptions(wsId: string, jobId: string) {
  return queryOptions({
    queryKey: importKeys.job(wsId, jobId),
    queryFn: () => api.getImportJob(wsId, jobId),
    refetchInterval: (query) => {
      const status = query.state.data?.status ?? "";
      return isTerminalImportStatus(status) ? false : 2000;
    },
  });
}
