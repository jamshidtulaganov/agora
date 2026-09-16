"use client";

import { useEffect } from "react";
import type { AssistantSession } from "@agora/core/types";
import { useAssistantStore } from "@agora/core/assistant";

/**
 * The persisted active session id can point at a session that no longer
 * exists (deleted on another device, or stale from a previous account) —
 * fall back to the most recently updated one once the list has loaded.
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

  useEffect(() => {
    if (!enabled) return;
    if (activeSessionId && sessions.some((s) => s.id === activeSessionId)) return;
    if (activeSessionId && sessions.length === 0) return; // list may still be loading
    if (sessions.length > 0) setActiveSession(sessions[0]!.id);
  }, [enabled, sessions, activeSessionId, setActiveSession]);
}
