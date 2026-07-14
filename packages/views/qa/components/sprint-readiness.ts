import type { SprintReadinessResponse } from "@agora/core/api/schemas";
import type { ProgressRingTone } from "@agora/ui/components/ui/progress-ring";
import { regressionStatusMeta } from "./regression-status";

// Shared readiness classification for one sprint — the single mapping from a
// sprint-readiness row to its ring fill, tone, and headline state. Both the
// Ship card (large ring + sub-label) and the health strip (mini ring + "ready"
// text) read this so the two surfaces can never disagree about how close a
// sprint is to shipping.
//
// Tone vocabulary (codebase norm): ready=emerald, close=amber, far=muted,
// blocked=destructive. Precedence: mergeable wins, then a real task failure
// blocks, then an in-flight/pending regression is "close", else "far".

type Sprint = SprintReadinessResponse["sprints"][number];

export type ReadinessState = "ready" | "blocking" | "regression" | "togo";

export interface SprintReadiness {
  /** Ring fill fraction, passed / total (0 when the sprint has no tasks). */
  value: number;
  tone: ProgressRingTone;
  state: ReadinessState;
  /** Count that goes with the state: failing tasks (blocking) or tasks left (togo). */
  count: number;
}

export function sprintReadiness(s: Sprint): SprintReadiness {
  const value = s.total > 0 ? Math.min(s.passed / s.total, 1) : 0;
  const togo = Math.max(0, s.total - s.passed);

  if (s.mergeable) {
    return { value, tone: "ready", state: "ready", count: 0 };
  }
  if (s.failed > 0) {
    return { value, tone: "blocked", state: "blocking", count: s.failed };
  }
  // No task failures, but a regression run that hasn't cleanly completed keeps
  // the sprint short of ready — amber "close", not muted "far".
  if (s.regression?.status) {
    const { done } = regressionStatusMeta(s.regression.status);
    if (!done) return { value, tone: "close", state: "regression", count: togo };
  }
  return { value, tone: "far", state: "togo", count: togo };
}
