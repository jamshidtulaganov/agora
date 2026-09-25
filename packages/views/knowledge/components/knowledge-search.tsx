"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";
import {
  KNOWLEDGE_SEARCH_MIN_CHARS,
  knowledgeSearchOptions,
  type KnowledgeSearchResult,
} from "@agora/core/knowledge";
import { Input } from "@agora/ui/components/ui/input";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { useT } from "../../i18n";

const SEARCH_DEBOUNCE_MS = 250;

/** `value`, settled for `delayMs` — so each keystroke doesn't hit the server. */
export function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(timer);
  }, [value, delayMs]);
  return debounced;
}

export function isKnowledgeSearchActive(query: string): boolean {
  return query.trim().length >= KNOWLEDGE_SEARCH_MIN_CHARS;
}

export function KnowledgeSearchInput({
  value,
  onChange,
}: {
  value: string;
  onChange: (value: string) => void;
}) {
  const { t } = useT("knowledge");
  return (
    <div className="relative">
      <Search
        className="pointer-events-none absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground"
        aria-hidden
      />
      <Input
        type="search"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Escape" && value) {
            e.preventDefault();
            onChange("");
          }
        }}
        aria-label={t(($) => $.search.label)}
        placeholder={t(($) => $.search.placeholder)}
        className="h-8 pl-8 text-sm"
      />
    </div>
  );
}

/** Ranked sections for `query` (already debounced by the caller). */
export function KnowledgeSearchResults({
  wsId,
  query,
  onOpen,
}: {
  wsId: string;
  query: string;
  onOpen: (result: KnowledgeSearchResult) => void;
}) {
  const { t } = useT("knowledge");
  const { data, isLoading, isError } = useQuery(knowledgeSearchOptions(wsId, query));

  if (isLoading) {
    return (
      <div className="space-y-2">
        {Array.from({ length: 3 }).map((_, i) => (
          <Skeleton key={i} className="h-14 w-full" />
        ))}
      </div>
    );
  }
  if (isError) {
    return <p className="py-6 text-center text-sm text-muted-foreground">{t(($) => $.search.failed)}</p>;
  }
  const results = data?.results ?? [];
  if (results.length === 0) {
    return (
      <p className="py-6 text-center text-sm text-muted-foreground">
        {t(($) => $.search.no_results, { query: query.trim() })}
      </p>
    );
  }

  return (
    <ul className="space-y-0.5">
      {results.map((result, index) => (
        <li key={result.chunk_id || `${result.doc_id}-${index}`}>
          <button
            type="button"
            onClick={() => onOpen(result)}
            className="-mx-2 block w-[calc(100%+1rem)] rounded-md px-2 py-2 text-left transition-colors hover:bg-accent/40 focus-visible:bg-accent/40 focus-visible:outline-none"
          >
            <div className="flex items-baseline justify-between gap-3">
              <span className="truncate text-sm font-medium">{result.doc_title}</span>
              {result.location && (
                <span className="shrink-0 text-[11px] text-muted-foreground">{result.location}</span>
              )}
            </div>
            {result.heading_path && (
              <p className="truncate text-[11px] text-muted-foreground">{result.heading_path}</p>
            )}
            {result.snippet && (
              <p className="mt-0.5 line-clamp-2 text-xs text-muted-foreground">{result.snippet}</p>
            )}
          </button>
        </li>
      ))}
    </ul>
  );
}

export { SEARCH_DEBOUNCE_MS };
