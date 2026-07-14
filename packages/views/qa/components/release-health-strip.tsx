"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Rocket, ShieldAlert } from "lucide-react";
import { useWorkspaceId } from "@agora/core/hooks";
import { qaQueueOptions, qaVerdictsOptions, sprintReadinessOptions } from "@agora/core/qa/queries";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { useT } from "../../i18n";
import { qaEffectiveState } from "./qa-lane";
import { useQaLiveIssueMap } from "./qa-live-progress";

// The Release page's always-on health strip — a single roll-up line so "can we
// ship?" stays visible from every non-Ship tab without opening Ship. Reads the
// same query factories as the Ship view and the Queue (one cache entry each,
// zero drift). The roll-up ("M of N sprints ready to ship") links to Ship,
// where the per-sprint detail lives; the needs-decision chip sits beside it and
// deep-links to Queue with the needs-human toggle pre-set. Hidden on the Ship
// tab (the SprintCards already carry the full readiness detail).

export function ReleaseHealthStrip({
  projectId,
  onOpenShip,
  onOpenQueueNeedsHuman,
}: {
  projectId?: string;
  onOpenShip: () => void;
  onOpenQueueNeedsHuman: () => void;
}) {
  const wsId = useWorkspaceId();
  const { t } = useT("issues");
  // Shared factories — the same cache entries the Ship view and the Queue read.
  const { data, isLoading } = useQuery(sprintReadinessOptions(wsId, projectId));
  const { data: queueData } = useQuery(qaQueueOptions(wsId, projectId));
  const { data: verdictData } = useQuery(qaVerdictsOptions(wsId, projectId));
  // The SAME live set the Queue's needs-human toggle subtracts — an issue
  // whose gate is running right now is "running", not "needs a decision", in
  // BOTH places, so the chip's count always equals the deep-linked row count.
  const { liveIssueIds } = useQaLiveIssueMap(wsId);

  // "K need a decision" — in_review issues whose effective state says a human
  // is the next step (the same definition the Queue's needs-human toggle cuts by).
  const needsDecision = useMemo(() => {
    const verdicts = verdictData?.verdicts ?? {};
    return (queueData?.issues ?? []).filter((i) => {
      const s = qaEffectiveState(i, verdicts, liveIssueIds);
      return s === "fail" || s === "pass_with_failing_cases";
    }).length;
  }, [queueData, verdictData, liveIssueIds]);

  const sprints = data?.sprints ?? [];
  const readyCount = sprints.filter((s) => s.mergeable).length;

  if (isLoading && !data) {
    return (
      <div className="border-b px-8 py-2" aria-hidden>
        <Skeleton className="h-7 w-full" />
      </div>
    );
  }

  if (sprints.length === 0) return null;

  return (
    <div className="border-b bg-muted/20 px-8 py-1.5">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px]">
        <button
          type="button"
          onClick={onOpenShip}
          title={t(($) => $.qa_cockpit.health_row_title)}
          className="flex items-center gap-1.5 rounded-md px-2 py-1 font-medium hover:bg-accent/60"
        >
          <Rocket className="size-3.5 text-muted-foreground" aria-hidden />
          {t(($) => $.qa_cockpit.health_rollup, { ready: readyCount, sprints: sprints.length })}
        </button>

        {needsDecision > 0 && (
          <button
            type="button"
            onClick={onOpenQueueNeedsHuman}
            className="flex items-center gap-1 rounded-full bg-destructive/10 px-2.5 py-1 text-[11px] font-medium text-destructive hover:bg-destructive/20"
          >
            <ShieldAlert className="size-3.5" aria-hidden />
            {t(($) => $.qa_cockpit.health_needs_decision, { count: needsDecision })}
          </button>
        )}
      </div>
    </div>
  );
}
