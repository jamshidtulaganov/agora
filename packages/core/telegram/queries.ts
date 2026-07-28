import { queryOptions } from "@tanstack/react-query";

import { api } from "../api";

/** Query keys for the per-agent Telegram bot surface.
 *
 * Workspace-scoped, so switching workspace changes the key and the right data
 * appears with no manual invalidation (CLAUDE.md → State Management). */
export const telegramKeys = {
  all: (wsId: string) => ["telegram", wsId] as const,
  installations: (wsId: string) => [...telegramKeys.all(wsId), "installations"] as const,
};

/** Shared options object rather than a hook: several panels read the same list
 * — the Integrations card, the tab body, the agent detail pane — and spreading
 * one factory keeps them on one cache entry instead of three fetches. */
export const telegramInstallationsOptions = (wsId: string) =>
  queryOptions({
    queryKey: telegramKeys.installations(wsId),
    queryFn: () => api.listTelegramInstallations(wsId),
    enabled: !!wsId,
  });
