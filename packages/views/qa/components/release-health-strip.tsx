"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  CheckCircle2,
  XCircle,
  CircleDashed,
  Rocket,
  ShieldAlert,
  ShieldCheck,
  Loader2,
} from "lucide-react";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core/hooks";
import type { SprintReadinessResponse } from "@agora/core/api/schemas";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { qaEffectiveState } from "./qa-lane";

// The Release page's always-on health strip — one compact row per active
// sprint (readiness rollup) so "can we ship?" is visible from every tab
// without opening Ship. Shares the exact query keys/options of the Ship view
// and the Queue (one cache entry each, zero drift). Rows click through to
// Ship; the needs-decision chip (fail / pass_with_failing_cases in the
// review queue) deep-links to Queue with the needs-human toggle pre-set.

// The strip has no live-task feed (that stays a Queue concern) — an empty
// live set makes qaEffectiveState fall back to labels, which the server's
// reconciled_state overrides whenever evidence exists.
const NO_LIVE: ReadonlySet<string> = new Set();

type Sprint = SprintReadinessResponse["sprints"][number];

function RegressionGlyph({ gate }: { gate: Sprint["regression"] }) {
  const { t } = useT("issues");
  if (!gate || !gate.status) return null;
  const done = gate.status === "completed" || gate.status === "succeeded";
  const failed = gate.status === "failed" || gate.status === "error";
  const Icon = failed ? ShieldAlert : done ? ShieldCheck : Loader2;
  return (
    <span
      className={cn(
        "flex shrink-0 items-center",
        failed ? "text-destructive" : done ? "text-emerald-500" : "text-muted-foreground",
      )}
      title={t(($) => $.qa_cockpit.sprint_regression_status, { status: gate.status })}
    >
      <Icon className={cn("size-3.5", !done && !failed && "animate-spin")} aria-hidden />
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
  const project = projectId ?? "all";
  // EXACT key + options of QASprintReadinessView — shared cache entry.
  const { data, isLoading } = useQuery({
    queryKey: ["qa-sprint-readiness", wsId, project],
    queryFn: () => api.getSprintReadiness(projectId),
    staleTime: 30_000,
    refetchInterval: 60_000,
  });
  // EXACT keys + options of the Queue's issue/verdict queries — shared cache.
  const { data: queueData } = useQuery({
    queryKey: ["qa-cockpit", wsId, project],
    queryFn: () =>
      api.listIssues({ status: "in_review", limit: 200, ...(project !== "all" ? { project_id: project } : {}) }),
    staleTime: 15_000,
  });
  const { data: verdictData } = useQuery({
    queryKey: ["qa-verdicts", wsId, project],
    queryFn: () => api.listQAVerdicts(project !== "all" ? project : undefined),
    staleTime: 15_000,
  });

  // "K need a decision" — in_review issues whose effective state says a human
  // is the next step (the same definition the Queue's needs-human toggle cuts by).
  const needsDecision = useMemo(() => {
    const verdicts = verdictData?.verdicts ?? {};
    return (queueData?.issues ?? []).filter((i) => {
      const s = qaEffectiveState(i, verdicts, NO_LIVE);
      return s === "fail" || s === "pass_with_failing_cases";
    }).length;
  }, [queueData, verdictData]);

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
