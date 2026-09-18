import { QueryClient } from "@tanstack/react-query";

import { ApiError } from "./api/client";

/** How many times a failed query is retried before the UI shows an error. */
const MAX_QUERY_RETRIES = 4;

/**
 * Retry policy tuned for a sleeping backend.
 *
 * The public web service cold-starts, and every request that lands in that
 * window fails. A single retry (the old policy) fired ~1s later — still inside
 * the cold start — so the query latched an error state that nothing clears
 * until the user finds a "try again" button, or a refetch happens to run. With
 * exponential backoff, MAX_QUERY_RETRIES spans ~30s and rides the boot out.
 *
 * Client errors are NOT retried: a 401/403/404 is an answer, not a blip, and
 * retrying it just delays the error the UI needs to show. 408 and 429 are the
 * exceptions — both explicitly mean "ask again later".
 */
function shouldRetry(failureCount: number, error: unknown): boolean {
  if (failureCount >= MAX_QUERY_RETRIES) return false;
  if (error instanceof ApiError) {
    const { status } = error;
    if (status === 408 || status === 429) return true;
    if (status >= 400 && status < 500) return false;
  }
  return true;
}

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: Infinity,
        gcTime: 10 * 60 * 1000, // 10 minutes
        refetchOnWindowFocus: false,
        refetchOnReconnect: true,
        retry: shouldRetry,
      },
      mutations: {
        // Still never retried: a mutation is not safe to replay blindly.
        retry: false,
      },
    },
  });
}
