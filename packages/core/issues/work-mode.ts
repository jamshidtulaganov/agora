// Per-issue "work mode" — how a human + agents collaborate on the issue.
//
//   full_pipeline : agent runs the full loop (plan → code → PR); the human
//                   steers via comments and reviews the result. (default)
//   in_editor     : the human co-codes live in the embedded editor on the
//                   agent's worktree — agents run there, the human watches
//                   changes and edits the code, mostly as reviewer.
//
// Stored as the `work_mode` key in the issue's `metadata` jsonb (a plain
// string value), so no schema change is needed and the backend can read it
// when building the agent's context (wired separately).

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { issueKeys } from "./queries";
import type { Issue } from "../types";

export const WORK_MODES = ["full_pipeline", "in_editor"] as const;
export type WorkMode = (typeof WORK_MODES)[number];

export const DEFAULT_WORK_MODE: WorkMode = "full_pipeline";

/** Reads the issue's work mode, defaulting to full_pipeline when unset. */
export function getWorkMode(
  issue: Pick<Issue, "metadata"> | null | undefined,
): WorkMode {
  return issue?.metadata?.work_mode === "in_editor"
    ? "in_editor"
    : "full_pipeline";
}

/**
 * Sets the issue's work mode via the single-key metadata endpoint. Patches the
 * detail cache from the returned metadata; the EventIssueMetadataChanged WS
 * event syncs the list/my-issues caches.
 */
export function useSetWorkMode(wsId: string, issueId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (mode: WorkMode) =>
      api.setIssueMetadataKey(issueId, "work_mode", mode),
    onSuccess: (res) => {
      qc.setQueryData<Issue>(issueKeys.detail(wsId, issueId), (old) =>
        old ? { ...old, metadata: res.metadata } : old,
      );
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: issueKeys.detail(wsId, issueId) });
    },
  });
}
