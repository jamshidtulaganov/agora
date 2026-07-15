"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { defaultStorage } from "../../platform/storage";

const MAX_RECENT_ISSUES = 20;
const MAX_WORKSPACES = 50;
const EMPTY: RecentIssueEntry[] = [];

export interface RecentIssueEntry {
  id: string;
  visitedAt: number;
}

interface RecentIssuesState {
  identityId: string | null;
  byWorkspace: Record<string, RecentIssueEntry[]>;
  setIdentity: (identityId: string | null) => void;
  recordVisit: (wsId: string, id: string) => void;
  forgetIssue: (wsId: string, id: string) => void;
  pruneWorkspaces: (activeWsIds: string[]) => void;
}

function bucketKey(identityId: string | null, wsId: string): string | null {
  return identityId ? `${identityId}:${wsId}` : null;
}

// Namespace by workspace id (UUID) instead of namespacing the storage key by
// slug. The storage-key approach (see createWorkspaceAwareStorage) breaks when
// a setter fires from a child component's mount-effect before
// WorkspaceRouteLayout's effect has set the current slug — child effects run
// before parent effects, so writes land in the un-namespaced bare key and
// leak across workspaces (bug surfaced by /<slug>/issues firing per-id GETs
// for recents from other workspaces, most returning 404).
//
// Keying on wsId (rather than slug) means the data survives workspace renames
// and matches the wsId that callers already have via useWorkspaceId().
export const useRecentIssuesStore = create<RecentIssuesState>()(
  persist(
    (set) => ({
      identityId: null,
      byWorkspace: {},
      setIdentity: (identityId) => set({ identityId }),
      recordVisit: (wsId, id) =>
        set((state) => {
          const key = bucketKey(state.identityId, wsId);
          if (!key) return state;
          const bucket = state.byWorkspace[key] ?? EMPTY;
          const filtered = bucket.filter((i) => i.id !== id);
          const updated: RecentIssueEntry = { id, visitedAt: Date.now() };
          const nextBucket = [updated, ...filtered].slice(0, MAX_RECENT_ISSUES);

          let nextByWorkspace = {
            ...state.byWorkspace,
            [key]: nextBucket,
          };

          // LRU defense: if pruneWorkspaces never gets a chance to run (offline,
          // failed list query) and the user touches lots of workspaces, cap the
          // total to avoid unbounded growth. Evict the workspace whose most
          // recent visit is the oldest.
          const ids = Object.keys(nextByWorkspace);
          if (ids.length > MAX_WORKSPACES) {
            const oldest = ids.reduce((oldestId, candidateId) => {
              const a = nextByWorkspace[oldestId]?.[0]?.visitedAt ?? 0;
              const b = nextByWorkspace[candidateId]?.[0]?.visitedAt ?? 0;
              return b < a ? candidateId : oldestId;
            });
            const { [oldest]: _, ...rest } = nextByWorkspace;
            nextByWorkspace = rest;
          }

          return { byWorkspace: nextByWorkspace };
        }),
      forgetIssue: (wsId, id) =>
        set((state) => {
          const key = bucketKey(state.identityId, wsId);
          if (!key) return state;
          const bucket = state.byWorkspace[key];
          if (!bucket) return state;
          const nextBucket = bucket.filter((entry) => entry.id !== id);
          if (nextBucket.length === bucket.length) return state;
          if (nextBucket.length === 0) {
            const { [key]: _, ...rest } = state.byWorkspace;
            return { byWorkspace: rest };
          }
          return {
            byWorkspace: { ...state.byWorkspace, [key]: nextBucket },
          };
        }),
      pruneWorkspaces: (activeWsIds) =>
        set((state) => {
          if (!state.identityId) return state;
          const allow = new Set(activeWsIds);
          let changed = false;
          const next: Record<string, RecentIssueEntry[]> = {};
          const prefix = `${state.identityId}:`;
          for (const [key, items] of Object.entries(state.byWorkspace)) {
            if (!key.startsWith(prefix) || allow.has(key.slice(prefix.length))) {
              next[key] = items;
            } else {
              changed = true;
            }
          }
          return changed ? { byWorkspace: next } : state;
        }),
    }),
    {
      name: "agora_recent_issues",
      storage: createJSONStorage(() => defaultStorage),
      partialize: (state) => ({ byWorkspace: state.byWorkspace }),
      // v0 stored a flat `items` array under the bare key (or, when the
      // workspace slug happened to be set at write time, under
      // `agora_recent_issues:<slug>`). Both shapes are unsafe to surface
      // because v0 entries don't know which workspace they belonged to —
      // drop them and let the cache repopulate as the user visits issues.
      // v1 was workspace-scoped but not actor-scoped. That leaked stale IDs
      // when two users shared a browser profile (or a local DB was replaced).
      // Drop it once; v2 buckets are keyed by user id + workspace id.
      version: 2,
      migrate: () => ({ byWorkspace: {} }),
    },
  ),
);

export function selectRecentIssues(wsId: string | null) {
  return (state: RecentIssuesState) =>
    wsId
      ? (state.byWorkspace[bucketKey(state.identityId, wsId) ?? ""] ?? EMPTY)
      : EMPTY;
}
