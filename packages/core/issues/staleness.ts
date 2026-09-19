import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import {
  KNOWN_STALE_REASONS,
  type KnownStaleReason,
  type StaleIssue,
  type StaleReason,
} from "../types";

/**
 * Staleness query — Tier 2 of docs/living-truth-plan.md.
 *
 * "This issue looks wrong" computed on read from what already exists (issue
 * / comment / task timestamps + linked PR state). Nothing is stored, so the
 * signal itself can never go stale; it is also never written back to the
 * issue — it only renders.
 *
 * Workspace-scoped like every other workspace read, so the key carries
 * `wsId`: switching workspaces swaps the cache entry with no manual
 * invalidation (CLAUDE.md "State Management").
 */
const KNOWN_REASONS: ReadonlySet<string> = new Set<string>(KNOWN_STALE_REASONS);

/**
 * Narrow a wire `reason` to one of the four rules we have copy for.
 * Anything else — a reason a newer server learned, or an empty string from a
 * partially-parsed row — falls through to the generic rendering.
 */
export function isKnownStaleReason(reason: StaleReason): reason is KnownStaleReason {
  return KNOWN_REASONS.has(reason);
}

export const stalenessKeys = {
  all: (wsId: string) => ["issue-staleness", wsId] as const,
  /** FULL KEY — `null` project means "whole workspace". */
  list: (wsId: string, projectId?: string) =>
    [...stalenessKeys.all(wsId), projectId ?? null] as const,
};

/**
 * Staleness rules measure in DAYS (idle ≥ 5d, review_done ≥ 2d,
 * blocked_quiet ≥ 7d), so the answer moves on the order of hours at best —
 * refetching it on every focus would be pure noise. 60s is a compromise that
 * keeps a manual reload honest while letting a tab switch reuse the cache.
 * Liveness comes from invalidation instead: the same WS issue events that
 * refresh the list drop this key too (see issues/ws-updaters.ts).
 */
export const STALENESS_STALE_TIME_MS = 60_000;

/**
 * `retry: false` and no error surface: staleness is a NUDGE. Against a
 * backend that doesn't serve this route yet (an installed desktop build is
 * always older or newer than the server it talks to) this is a hard 404, and
 * the right behavior is to render nothing — not to hammer, and not to show
 * an error where a small clock glyph would have been.
 */
export function staleIssuesOptions(wsId: string, projectId?: string) {
  return queryOptions({
    queryKey: stalenessKeys.list(wsId, projectId),
    queryFn: () => api.getIssueStaleness(projectId),
    enabled: !!wsId,
    staleTime: STALENESS_STALE_TIME_MS,
    retry: false,
    select: toStaleIssueMap,
  });
}

/**
 * Memoized per response object. Every row that renders an indicator reads
 * this same query (TanStack dedupes the request), so `select` runs once per
 * render per row — without the cache a 200-row board would rebuild the map
 * 200 times, and each rebuild would hand the row a new Map identity.
 */
const MAP_CACHE = new WeakMap<{ stale: StaleIssue[] }, Map<string, StaleIssue>>();

/**
 * Client-side merge key: `Map<issue_id, StaleIssue>`. The list/board join
 * happens here rather than in a server join, so the hot list path stays
 * exactly as fast as it was.
 *
 * Rows with an empty `issue_id` are dropped — the schema defaults every
 * field so a partial row still parses, but a row that can't be matched to an
 * issue has nothing to decorate.
 */
export function toStaleIssueMap(data: { stale: StaleIssue[] }): Map<string, StaleIssue> {
  const cached = MAP_CACHE.get(data);
  if (cached) return cached;
  const map = new Map<string, StaleIssue>();
  for (const row of data.stale) {
    if (!row.issue_id) continue;
    map.set(row.issue_id, row);
  }
  MAP_CACHE.set(data, map);
  return map;
}
