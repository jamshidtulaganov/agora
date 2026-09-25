import { z } from "zod";
import { useMutation, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { DepartmentSetupStatus, Workspace } from "../types";
import { workspaceKeys } from "./queries";

// Department setup: an owner/admin sets a workspace up for their team once —
// knowledge, what the team's sidebar shows, a few ready-made agents — or skips
// to the default. Two small workspace.settings keys carry the result (see
// server/internal/handler/workspace_setup.go):
//
//   settings.team_sidebar     = { hidden: [nav keys], updated_by, updated_at }
//   settings.department_setup = { status: "done" | "skipped", by, at }
//
// `settings` is an untyped JSON bag that any server version (or a hand edit)
// can fill with anything, so both keys are read through zod and a malformed
// value reads as "not set" instead of throwing into the UI.

export const TEAM_SIDEBAR_SETTING_KEY = "team_sidebar";
export const DEPARTMENT_SETUP_SETTING_KEY = "department_setup";

const TeamSidebarSettingSchema = z
  .object({
    // Non-string entries are dropped rather than failing the whole value: one
    // bad key shouldn't throw the team back to the full sidebar.
    hidden: z
      .array(z.unknown())
      .transform((keys) => keys.filter((k): k is string => typeof k === "string")),
  })
  .loose();

const DepartmentSetupSettingSchema = z
  .object({
    status: z.enum(["done", "skipped"]),
  })
  .loose();

function settingValue(settings: unknown, key: string): unknown {
  if (!settings || typeof settings !== "object" || Array.isArray(settings)) return undefined;
  return (settings as Record<string, unknown>)[key];
}

/**
 * The nav keys the workspace's team sidebar hides, or null when the workspace
 * has no team sidebar (or the stored value is unreadable). `[]` is a real
 * answer: the admin chose to show everything.
 */
export function readTeamSidebar(settings: unknown): string[] | null {
  const parsed = TeamSidebarSettingSchema.safeParse(settingValue(settings, TEAM_SIDEBAR_SETTING_KEY));
  return parsed.success ? parsed.data.hidden : null;
}

/**
 * Whether the department setup was finished or skipped, or null when nobody
 * decided yet. An unknown status (a newer server's value) also reads as null.
 */
export function readDepartmentSetup(settings: unknown): { status: DepartmentSetupStatus } | null {
  const parsed = DepartmentSetupSettingSchema.safeParse(
    settingValue(settings, DEPARTMENT_SETUP_SETTING_KEY),
  );
  return parsed.success ? { status: parsed.data.status } : null;
}

type WorkspaceListContext = { previous: Workspace[] | undefined };

/**
 * Optimistically writes one settings key on the cached workspace, returning
 * the previous list for rollback. Only the named key changes, mirroring the
 * server's key-scoped write, so sibling settings are never clobbered.
 */
async function patchCachedSetting(
  qc: QueryClient,
  workspaceId: string,
  key: string,
  value: Record<string, unknown>,
): Promise<WorkspaceListContext> {
  await qc.cancelQueries({ queryKey: workspaceKeys.list() });
  const previous = qc.getQueryData<Workspace[]>(workspaceKeys.list());
  qc.setQueryData<Workspace[]>(workspaceKeys.list(), (old) =>
    old?.map((ws) =>
      ws.id === workspaceId ? { ...ws, settings: { ...(ws.settings ?? {}), [key]: value } } : ws,
    ),
  );
  return { previous };
}

function adoptWorkspace(qc: QueryClient, workspaceId: string, updated: Workspace) {
  // A drifted body parses to EMPTY_WORKSPACE (id ""); only trust a response
  // that is the workspace we changed, otherwise keep the optimistic value
  // until the settle refetch.
  if (updated?.id !== workspaceId) return;
  qc.setQueryData<Workspace[]>(workspaceKeys.list(), (old) =>
    old?.map((ws) => (ws.id === workspaceId ? updated : ws)),
  );
}

/** Saves the sidebar members get until they set their own (owner/admin). */
export function useUpdateTeamSidebar() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ workspaceId, hidden }: { workspaceId: string; hidden: string[] }) =>
      api.updateTeamSidebar(workspaceId, hidden),
    onMutate: ({ workspaceId, hidden }) =>
      patchCachedSetting(qc, workspaceId, TEAM_SIDEBAR_SETTING_KEY, { hidden }),
    onError: (_err, _vars, ctx) => {
      if (ctx?.previous) qc.setQueryData(workspaceKeys.list(), ctx.previous);
    },
    onSuccess: (updated, { workspaceId }) => adoptWorkspace(qc, workspaceId, updated),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: workspaceKeys.list() });
    },
  });
}

/** Records that the department setup was finished or skipped (owner/admin). */
export function useSetDepartmentSetup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ workspaceId, status }: { workspaceId: string; status: DepartmentSetupStatus }) =>
      api.setDepartmentSetup(workspaceId, status),
    onMutate: ({ workspaceId, status }) =>
      patchCachedSetting(qc, workspaceId, DEPARTMENT_SETUP_SETTING_KEY, { status }),
    onError: (_err, _vars, ctx) => {
      if (ctx?.previous) qc.setQueryData(workspaceKeys.list(), ctx.previous);
    },
    onSuccess: (updated, { workspaceId }) => adoptWorkspace(qc, workspaceId, updated),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: workspaceKeys.list() });
    },
  });
}
