"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { CheckCircle2, XCircle, CircleDashed, Rocket, ShieldAlert } from "lucide-react";
import { useWorkspaceId } from "@agora/core/hooks";
import { qaQueueOptions, qaVerdictsOptions, sprintReadinessOptions } from "@agora/core/qa/queries";
import type { SprintReadinessResponse } from "@agora/core/api/schemas";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { qaEffectiveState } from "./qa-lane";
import { useQaLiveIssueMap } from "./qa-live-progress";
import { regressionStatusMeta } from "./regression-status";

// The Release page's always-on health strip — one compact row per active
// sprint (readiness rollup) so "can we ship?" is visible from every tab
// without opening Ship. Reads the same query factories as the Ship view and
// the Queue (one cache entry each, zero drift). Rows click through to
// Ship; the needs-decision chip (fail / pass_with_failing_cases in the
// review queue) deep-links to Queue with the needs-human toggle pre-set.

type Sprint = SprintReadinessResponse["sprints"][number];

function RegressionGlyph({ gate }: { gate: Sprint["regression"] }) {
  const { t } = useT("issues");
  if (!gate || !gate.status) return null;
  const { running, Icon, className } = regressionStatusMeta(gate.status);
  return (
    <span
      className={cn("flex shrink-0 items-center", className)}
      title={t(($) => $.qa_cockpit.sprint_regression_status, { status: gate.status })}
    >
      <Icon className={cn("size-3.5", running && "animate-spin")} aria-hidden />
    </span>
  );
}

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

  if (isLoading && !data) {
    return (
      <div className="border-b px-8 py-2" aria-hidden>
        <Skeleton className="h-7 w-full" />
      </div>
    );
  }

  const sprints = data?.sprints ?? [];
  if (sprints.length === 0) return null;

  return (
    <div className="flex items-start gap-3 border-b bg-muted/20 px-8 py-1.5">
      <div className="flex min-w-0 flex-1 flex-col">
        {sprints.map((s) => (
          <button
            key={s.sprint_id}
            type="button"
            onClick={onOpenShip}
            title={t(($) => $.qa_cockpit.health_row_title)}
            className="flex w-full items-center gap-3 rounded-md px-2 py-1 text-left text-[12px] hover:bg-accent/60"
          >
            <span className="min-w-0 truncate font-medium">
              {s.project_title} · {s.name}
            </span>
            <span className="ml-auto flex shrink-0 items-center gap-3">
              <RegressionGlyph gate={s.regression} />
              <span className="flex items-center gap-1 text-emerald-500" title={t(($) => $.qa_cockpit.sprint_passed_title)}>
                <CheckCircle2 className="size-3.5" aria-hidden /> {s.passed}
              </span>
              <span className="flex items-center gap-1 text-destructive" title={t(($) => $.qa_cockpit.sprint_failing_title)}>
                <XCircle className="size-3.5" aria-hidden /> {s.failed}
              </span>
              <span
                className="flex items-center gap-1 text-muted-foreground"
                title={t(($) => $.qa_cockpit.sprint_pending_title, { count: s.no_qa })}
              >
                <CircleDashed className="size-3.5" aria-hidden /> {s.pending}
              </span>
              <span
                className={
                  s.mergeable
                    ? "flex items-center gap-1 rounded-full bg-emerald-500/15 px-2.5 py-0.5 text-[11px] font-medium text-emerald-500"
                    : "flex items-center gap-1 rounded-full bg-muted px-2.5 py-0.5 text-[11px] font-medium text-muted-foreground"
                }
              >
                <Rocket className="size-3.5" aria-hidden />
                {s.mergeable ? t(($) => $.qa_cockpit.sprint_mergeable) : t(($) => $.qa_cockpit.sprint_not_ready)}
              </span>
            </span>
          </button>
        ))}
      </div>
      {needsDecision > 0 && (
        <button
          type="button"
          onClick={onOpenQueueNeedsHuman}
          className="mt-1 flex shrink-0 items-center gap-1 rounded-full bg-destructive/10 px-2.5 py-1 text-[11px] font-medium text-destructive hover:bg-destructive/20"
        >
          <ShieldAlert className="size-3.5" aria-hidden />
          {t(($) => $.qa_cockpit.health_needs_decision, { count: needsDecision })}
        </button>
      )}
    </div>
  );
}
