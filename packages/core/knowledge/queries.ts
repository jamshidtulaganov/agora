import { queryOptions, type QueryClient } from "@tanstack/react-query";
import { api } from "../api";

// Every knowledge read is workspace-scoped: the key carries wsId so switching
// workspace swaps the cache instead of showing another team's documents
// (CLAUDE.md "Workspace-scoped queries must key on wsId"). Freshness comes from
// the `knowledge:updated` WS event, not polling — a document that finishes
// reading arrives as an invalidation.

export const KNOWLEDGE_SEARCH_MIN_CHARS = 2;
export const KNOWLEDGE_SEARCH_LIMIT = 10;

export const knowledgeKeys = {
  all: (wsId: string) => ["knowledge", wsId] as const,
  list: (wsId: string) => [...knowledgeKeys.all(wsId), "list"] as const,
  detail: (wsId: string, id: string) => [...knowledgeKeys.all(wsId), "detail", id] as const,
  search: (wsId: string, query: string, limit: number) =>
    [...knowledgeKeys.all(wsId), "search", query, limit] as const,
};

export function knowledgeListOptions(wsId: string) {
  return queryOptions({
    queryKey: knowledgeKeys.list(wsId),
    queryFn: () => api.listKnowledge(),
  });
}

export function knowledgeDocOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: knowledgeKeys.detail(wsId, id),
    queryFn: () => api.getKnowledgeDoc(id),
    enabled: id !== "",
  });
}

/** Disabled below KNOWLEDGE_SEARCH_MIN_CHARS so a single keystroke doesn't search. */
export function knowledgeSearchOptions(wsId: string, query: string, limit = KNOWLEDGE_SEARCH_LIMIT) {
  const q = query.trim();
  return queryOptions({
    queryKey: knowledgeKeys.search(wsId, q, limit),
    queryFn: () => api.searchKnowledge(q, limit),
    enabled: q.length >= KNOWLEDGE_SEARCH_MIN_CHARS,
    staleTime: 30_000,
    // Keep the previous results on screen while the next query loads, so the
    // list doesn't blink to a skeleton on every debounced keystroke.
    placeholderData: (previous) => previous,
  });
}

/**
 * `knowledge:updated` handler: a document was added, finished or failed
 * reading, was pinned/renamed or removed. One blanket invalidation covers the
 * list, the open viewer and any search results that quoted it.
 */
export function onKnowledgeUpdated(qc: QueryClient, wsId: string): void {
  void qc.invalidateQueries({ queryKey: knowledgeKeys.all(wsId) });
}
