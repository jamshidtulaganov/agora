"use client";

import { useCallback, useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  assistantMessagesOptions,
  assistantRunsOptions,
  useAssistantStore,
} from "@agora/core/assistant";
import { autoOpenDecision, isArtifactUpdating, runArtifactId } from "./lib/artifact-workbench";

const ACTIVE_STATUSES = new Set(["queued", "running"]);

export interface ArtifactWorkbench {
  /** Artifact the pane is showing for this session, or null. */
  openArtifactId: string | null;
  /** Open / switch / close the pane. Spends the current run's auto-open. */
  openArtifact: (artifactId: string | null) => void;
  /** The open artifact is being rewritten by the run in flight. */
  isUpdating: boolean;
}

/**
 * Wires the artifact pane to the run in flight: it opens itself on the first
 * artifact of a turn, and flags itself as updating while that turn is still
 * writing to it.
 *
 * Reads the SAME two queries the transcript renders from (messages + runs) —
 * these are cache subscriptions, not extra requests — and reacts to whatever
 * the WS invalidation last pulled in. `enabled` is what keeps this to the
 * assistant PAGE: the floating panel has nowhere to put a pane, so it must
 * never auto-open one.
 */
export function useArtifactWorkbench(
  sessionId: string | null,
  enabled: boolean,
): ArtifactWorkbench {
  const setOpenArtifact = useAssistantStore((s) => s.setOpenArtifact);
  // A primitive selector — a derived object here would re-render the page on
  // every store write (CLAUDE.md, Zustand footguns).
  const openArtifactId = useAssistantStore((s) =>
    sessionId ? (s.openArtifactId[sessionId] ?? null) : null,
  );

  const { data: messages = [] } = useQuery({
    ...assistantMessagesOptions(sessionId ?? ""),
    enabled: enabled && !!sessionId,
  });
  const { data: runs = [] } = useQuery({
    ...assistantRunsOptions(sessionId ?? ""),
    enabled: enabled && !!sessionId,
  });

  const latestRun = runs.at(-1) ?? null;
  const producedArtifactId = runArtifactId(messages, latestRun?.message_id);

  // The run whose auto-open has been used, scoped to the session it was used
  // in — switching chats hands the new session its own budget.
  const spentRef = useRef<{ sessionId: string; runId: string } | null>(null);
  // Read at click time, not render time, so a manual open always spends the
  // run that is actually in flight.
  const latestRunIdRef = useRef<string | null>(null);
  latestRunIdRef.current = latestRun?.id ?? null;

  useEffect(() => {
    if (!enabled || !sessionId) return;
    const spent = spentRef.current;
    const decision = autoOpenDecision({
      runId: latestRun?.id ?? null,
      artifactId: producedArtifactId,
      lastAutoOpenedRunId: spent?.sessionId === sessionId ? spent.runId : null,
      openArtifactId,
    });
    if (!decision.spentRunId) return;
    spentRef.current = { sessionId, runId: decision.spentRunId };
    if (decision.artifactId) setOpenArtifact(sessionId, decision.artifactId);
  }, [enabled, sessionId, latestRun?.id, producedArtifactId, openArtifactId, setOpenArtifact]);

  const openArtifact = useCallback(
    (artifactId: string | null) => {
      if (!sessionId) return;
      // The user chose this view. Spend the run's auto-open so the agent's
      // next artifact can't pull the pane out from under them.
      const runId = latestRunIdRef.current;
      if (runId) spentRef.current = { sessionId, runId };
      setOpenArtifact(sessionId, artifactId);
    },
    [sessionId, setOpenArtifact],
  );

  const isUpdating = isArtifactUpdating({
    isRunning: !!latestRun && ACTIVE_STATUSES.has(latestRun.status),
    activeTool: latestRun?.active_tool,
    openArtifactId,
    runArtifactId: producedArtifactId,
  });

  return { openArtifactId, openArtifact, isUpdating };
}
