import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { projectKeys } from "./queries";
import {
  DEFAULT_RISK_MAP_MAX_ENTRIES,
  DEFAULT_RISK_MAP_TIERS,
  type RiskMapEntry,
  type RiskMapResponse,
} from "../types";

/**
 * Project risk map reads + the write that made it a first-class object
 * (docs/orchestration-upgrade-plan.md §A1.2). The map is a list of MODULE
 * entries; the tier vocabulary and the entry cap both come from the server.
 */
export const riskMapKeys = {
  detail: (wsId: string, projectId: string) =>
    [...projectKeys.detail(wsId, projectId), "risk-map"] as const,
};

export function projectRiskMapOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: riskMapKeys.detail(wsId, projectId),
    queryFn: () => api.getProjectRiskMap(projectId),
    enabled: !!wsId && !!projectId,
  });
}

/**
 * Whole-list write.
 *
 * NOT optimistic, deliberately: this is a safety control, not a UI toggle.
 * The mutation settles on the server's own answer (`onSuccess` seeds the
 * cache with what was STORED — it may normalise globs, reorder entries, or
 * keep a tier this client never sent) and then invalidates on settle, so a
 * rejected or partially-applied save can never leave the editor showing
 * modules the pipeline is not using.
 */
export function useUpdateProjectRiskMap(wsId: string, projectId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (entries: RiskMapEntry[]) => api.updateProjectRiskMap(projectId, entries),
    onSuccess: (stored) => {
      qc.setQueryData<RiskMapResponse>(riskMapKeys.detail(wsId, projectId), stored);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: riskMapKeys.detail(wsId, projectId) });
    },
  });
}

/**
 * The tier choices the editor offers. The server's `tiers` is the truth; the
 * defaults are the floor so an older or drifted response still renders a
 * usable picker. A tier already in use by an entry is always offered, even
 * when the server did not list it — otherwise editing a neighbouring entry
 * would silently retier this one.
 */
export function riskMapTierOptions(data: RiskMapResponse | undefined): string[] {
  const names: string[] = [];
  const push = (tier: string) => {
    if (tier && !names.includes(tier)) names.push(tier);
  };
  for (const tier of data?.tiers ?? []) push(tier);
  if (names.length === 0) for (const tier of DEFAULT_RISK_MAP_TIERS) push(tier);
  for (const entry of data?.risk_map ?? []) push(entry.tier);
  return names;
}

/** What a path no entry matches is treated as. */
export function riskMapDefaultTier(data: RiskMapResponse | undefined): string {
  return data?.default_tier || "guarded";
}

/** The server's cap, with a floor so the editor always has a stop. */
export function riskMapMaxEntries(data: RiskMapResponse | undefined): number {
  const max = data?.max_entries ?? 0;
  return max > 0 ? max : DEFAULT_RISK_MAP_MAX_ENTRIES;
}

/**
 * Clean the draft before writing: trim, drop blank globs, and drop an entry
 * that names nothing and matches nothing. An entry with a module name but no
 * paths is kept — that is a half-written entry the person can see and
 * finish, not something to silently delete on their behalf.
 */
export function pruneRiskMapEntries(entries: RiskMapEntry[]): RiskMapEntry[] {
  const cleaned: RiskMapEntry[] = [];
  for (const entry of entries) {
    const paths = entry.paths.map((p) => p.trim()).filter((p) => p.length > 0);
    const module = entry.module.trim();
    if (!module && paths.length === 0) continue;
    cleaned.push({
      module,
      tier: entry.tier,
      paths,
      owner: entry.owner.trim(),
      notes: entry.notes.trim(),
    });
  }
  return cleaned;
}
