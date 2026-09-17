"use client";

import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@agora/core/api";
import type { AssistantSession } from "@agora/core/types";
import { assistantSessionOptions, useAssistantStore } from "@agora/core/assistant";

/**
 * The persisted active session id can point at a session that no longer
 * exists (deleted on another device, or stale from a previous account) —
 * fall back to the most recently updated one only after its detail endpoint
 * confirms it is gone. The list is capped, so absence from it does not prove
 * that an older session was deleted (artifact cards can open those sessions).
 *
 * Shared by the full page and the floating panel; both may be mounted at
 * once, and the correction is idempotent so running it twice is harmless.
 */
export function useHealActiveAssistantSession(
  sessions: AssistantSession[],
  enabled: boolean,
) {
  const activeSessionId = useAssistantStore((s) => s.activeSessionId);
  const setActiveSession = useAssistantStore((s) => s.setActiveSession);
  const listed = !!activeSessionId && sessions.some((s) => s.id === activeSessionId);
  const detail = useQuery({
    ...assistantSessionOptions(activeSessionId ?? ""),
    enabled: enabled && !!activeSessionId && !listed,
    retry: false,
    refetchOnMount: "always",
  });

  useEffect(() => {
    if (!enabled) return;
    if (!activeSessionId) {
      if (sessions.length > 0) setActiveSession(sessions[0]!.id);
      return;
    }
    if (listed || detail.isPending) return;
    if (detail.isError) {
      if (!(detail.error instanceof ApiError) || (detail.error.status !== 403 && detail.error.status !== 404)) return;
    }
    if (detail.data?.id === activeSessionId) return;
    setActiveSession(sessions[0]?.id ?? null);
  }, [enabled, sessions, activeSessionId, listed, detail.isPending, detail.isError, detail.error, detail.data?.id, setActiveSession]);
}
