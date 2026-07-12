"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { CheckCircle2, XCircle, CircleDashed, ShieldAlert } from "lucide-react";
import { useWorkspaceId } from "@agora/core/hooks";
import { qaQueueOptions, qaVerdictsOptions, sprintReadinessOptions } from "@agora/core/qa/queries";
import type { SprintReadinessResponse } from "@agora/core/api/schemas";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { ProgressRing, type ProgressRingTone } from "@agora/ui/components/ui/progress-ring";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { qaEffectiveState } from "./qa-lane";
import { useQaLiveIssueMap } from "./qa-live-progress";
import { regressionStatusMeta } from "./regression-status";
import { sprintReadiness } from "./sprint-readiness";

// The Release page's always-on health strip — one compact row per active
// sprint (readiness rollup) so "can we ship?" is visible from every tab
// without opening Ship. Reads the same query factories as the Ship view and
// the Queue (one cache entry each, zero drift). Rows are sorted
// closest-to-shipping first, carry a mini readiness ring + "N/M ready"
// headline, and click through to Ship; the needs-decision chip sits inline in
// the cluster and deep-links to Queue with the needs-human toggle pre-set.

type Sprint = SprintReadinessResponse["sprints"][number];

const READY_TEXT_TONE: Record<ProgressRingTone, string> = {
  ready: "text-emerald-600 dark:text-emerald-400",
  close: "text-amber-600 dark:text-amber-400",
  far: "text-muted-foreground",
  blocked: "text-destructive",
};

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

  // Closest-to-shipping first: mergeable sprints lead, then by passed ratio —
  // the strip reads top-to-bottom as "what ships next".
  const sprints = useMemo(() => {
    const rows = data?.sprints ?? [];
    return [...rows].sort((a, b) => {
      if (a.mergeable !== b.mergeable) return a.mergeable ? -1 : 1;
      const ra = a.total > 0 ? a.passed / a.total : 0;
      const rb = b.total > 0 ? b.passed / b.total : 0;
      return rb - ra;
    });
  }, [data]);

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
      <div className="flex flex-col gap-0.5">
        {sprints.map((s) => {
          const r = sprintReadiness(s);
          return (
            <button
              key={s.sprint_id}
              type="button"
              onClick={onOpenShip}
              title={t(($) => $.qa_cockpit.health_row_title)}
              className="flex w-full items-center gap-3 rounded-md px-2 py-1 text-left text-[12px] hover:bg-accent/60"
            >
              <ProgressRing
                value={r.value}
                size={18}
                strokeWidth={3}
                tone={r.tone}
                aria-label={t(($) => $.qa_cockpit.ring_aria, { passed: s.passed, total: s.total })}
              />
              <span className="min-w-0 flex-1 truncate font-medium">
                {s.project_title} · {s.name}
              </span>
              <span className={cn("shrink-0 text-[11px] font-medium tabular-nums", READY_TEXT_TONE[r.tone])}>
                {t(($) => $.qa_cockpit.health_ready, { passed: s.passed, total: s.total })}
              </span>
              <span className="flex shrink-0 items-center gap-3">
                <RegressionGlyph gate={s.regression} />
                <span className="flex items-center gap-1 text-emerald-500" title={t(($) => $.qa_cockpit.sprint_passed_title)}>
                  <CheckCircle2 className="size-3.5" aria-hidden /> {s.passed}
                </span>
                {s.failed > 0 ? (
                  <span className="flex items-center gap-1 text-destructive" title={t(($) => $.qa_cockpit.sprint_failing_title)}>
                    <XCircle className="size-3.5" aria-hidden /> {s.failed}
                  </span>
                ) : null}
                {s.pending > 0 ? (
                  <span
                    className="flex items-center gap-1 text-muted-foreground"
                    title={t(($) => $.qa_cockpit.sprint_pending_title, { count: s.no_qa })}
                  >
                    <CircleDashed className="size-3.5" aria-hidden /> {s.pending}
                  </span>
                ) : null}
              </span>
            </button>
          );
        })}

        {needsDecision > 0 && (
          <button
            type="button"
            onClick={onOpenQueueNeedsHuman}
            className="mt-0.5 flex w-fit items-center gap-1 self-start rounded-full bg-destructive/10 px-2.5 py-1 text-[11px] font-medium text-destructive hover:bg-destructive/20"
          >
            <ShieldAlert className="size-3.5" aria-hidden />
            {t(($) => $.qa_cockpit.health_needs_decision, { count: needsDecision })}
          </button>
        )}
      </div>
    </div>
  );
}
