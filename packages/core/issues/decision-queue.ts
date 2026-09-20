import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import {
  KNOWN_DECISION_QUEUE_KINDS,
  type DecisionQueueItem,
  type DecisionQueueKind,
  type DecisionQueueResponse,
} from "../types";

/**
 * The decision queue — docs/orchestration-upgrade-plan.md §A2.
 *
 * One ranked list of everything waiting on a human: agent questions, changes
 * ready to merge, failed QA verdicts and failed reviews. Rows arrive sorted
 * by `score` descending, computed on read server-side, so
 * this client NEVER re-sorts the rows — the server's order IS the ranking,
 * and a client that re-sorted would silently disagree with the phone.
 *
 * Workspace-scoped like every other workspace read, so the key carries
 * `wsId`: switching workspaces swaps the cache entry with no manual
 * invalidation (CLAUDE.md "State Management").
 */
const KNOWN_KINDS: ReadonlySet<string> = new Set<string>(KNOWN_DECISION_QUEUE_KINDS);

/**
 * Narrow a wire `kind` to one this build has a glyph and copy for. Anything
 * else — a kind a newer server learned — falls through to the generic row,
 * which still names the issue and still opens it.
 */
export function isKnownDecisionKind(kind: string): kind is DecisionQueueKind {
  return KNOWN_KINDS.has(kind);
}

export const decisionQueueKeys = {
  all: (wsId: string) => ["decision-queue", wsId] as const,
  /** FULL KEY — `null` project means "whole workspace". */
  list: (wsId: string, projectId?: string) =>
    [...decisionQueueKeys.all(wsId), projectId ?? null] as const,
};

/**
 * 30s. The queue's contents change on human and agent actions that ALREADY
 * broadcast — an escalation opening, a label flipping, an issue moving — and
 * those drop this key through the issue sweep (issues/ws-updaters.ts), so
 * liveness comes from invalidation, not from polling. The window exists only
 * so a tab switch or a second mount reuses the cache instead of refetching a
 * ranked read that touches every open issue; longer would let a queue a
 * colleague just cleared sit visibly wrong on an idle tab.
 */
export const DECISION_QUEUE_STALE_TIME_MS = 30_000;

export function decisionQueueOptions(wsId: string, projectId?: string) {
  return queryOptions({
    queryKey: decisionQueueKeys.list(wsId, projectId),
    queryFn: () => api.getDecisionQueue(projectId),
    enabled: !!wsId,
    staleTime: DECISION_QUEUE_STALE_TIME_MS,
  });
}

/** What the header states. EXACT counts and ages — never rates (§D2). */
export interface DecisionQueueSummary {
  /** How many decisions are waiting. */
  waiting: number;
  /** Age of the oldest waiting decision, in seconds. 0 when unknown. */
  oldestAgeSeconds: number;
}

/**
 * Age of one row, in seconds, from whichever of the two spellings the server
 * sent. `since` wins when both are present: it survives a response sitting in
 * the cache, where a precomputed `age_hours` freezes.
 */
export function decisionItemAgeSeconds(item: DecisionQueueItem, nowMs: number = Date.now()): number {
  if (item.since) {
    const started = new Date(item.since).getTime();
    if (Number.isFinite(started)) return Math.max(0, Math.round((nowMs - started) / 1000));
  }
  return item.age_hours > 0 ? Math.round(item.age_hours * 3600) : 0;
}

/**
 * Header numbers, combining the server's `total` with what the rows
 * themselves say. Two signals rather than one: a backend that forgets the
 * total (or zeroes it) must not blank the header, and a `total` larger than
 * the listed rows is the truth when the list is capped (CLAUDE.md: "Don't
 * pin a UI affordance to a single backend field").
 *
 * There is no oldest-age field on the wire, so the oldest age is derived
 * from the rows — which is also the only number that stays honest while the
 * response sits in the cache.
 */
export function summarizeDecisionQueue(
  data: DecisionQueueResponse | undefined,
  nowMs: number = Date.now(),
): DecisionQueueSummary {
  const items = data?.items ?? [];
  const waiting = Math.max(data?.total ?? 0, items.length);
  let oldest = 0;
  for (const item of items) {
    const age = decisionItemAgeSeconds(item, nowMs);
    if (age > oldest) oldest = age;
  }
  return { waiting, oldestAgeSeconds: oldest };
}

/**
 * Rows that can actually be opened. A row whose `issue_id` did not survive
 * the parse has nothing to point at — the schema fills every field so a
 * partial row still lists, but an anonymous row is a dead end, and a dead end
 * in this list is worse than one fewer row.
 */
export function usableDecisionItems(data: DecisionQueueResponse | undefined): DecisionQueueItem[] {
  return (data?.items ?? []).filter((item) => !!item.issue_id);
}
