"use client";

import { useQuery } from "@tanstack/react-query";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core";
import { issueDetailOptions } from "@agora/core/issues/queries";
import type { MergeGateStatus } from "@agora/core/types";
import { Badge } from "@agora/ui/components/ui/badge";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { PullRequestList } from "./pull-request-list";
import { verdictIcon, verdictTone } from "../../qa/components/verdict";

// The Review lens — a thin, read-only rollup of merge readiness (the same
// deterministic gate spine the editor's review bar polls, see
// editor-gates.tsx) plus the issue's PR list (docs/sdlc-stage-cockpit-plan.md,
// phase E). v1 is intentionally read-only: no merge/override mutation
// buttons here — merge actions stay agent-side/GitHub for now. Shares the
// ["merge-readiness", issueId] query key with EditorGates so the cache (and
// the 15s poll) is shared, not duplicated.

function GateCard({ gate }: { gate: MergeGateStatus }) {
  const { t } = useT("issues");
  const label =
    gate.status === "pass"
      ? t(($) => $.qa_evidence.verdict_pass)
      : gate.status === "fail"
        ? t(($) => $.qa_evidence.verdict_fail)
        : t(($) => $.qa_evidence.verdict_unknown);

  return (
    <div className={cn("rounded-lg border px-3 py-2", verdictTone(gate.status))}>
      <div className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        {gate.name}
      </div>
      <div className="mt-1 flex items-center gap-1.5 text-xs font-medium">
        {verdictIcon(gate.status, "size-3.5 shrink-0")}
        {label}
      </div>
    </div>
  );
}

export function ReviewLensBody({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceId();
  const { t } = useT("issues");

  const { data: issue } = useQuery(issueDetailOptions(wsId, issueId));
  const { data: readiness, isLoading } = useQuery({
    queryKey: ["merge-readiness", issueId],
    queryFn: () => api.mergeReadiness(issueId),
    enabled: !!issueId,
    refetchInterval: 15000,
  });

  const hasOverride = (issue?.labels ?? []).some((l) => l.name === "merge:override");

  return (
    <div className="mx-auto w-full max-w-4xl px-8 py-8">
      <div className="[&>*+*]:mt-8 [&>*+*]:border-t [&>*+*]:pt-8">
        <section>
          <div className="mb-2 flex items-center gap-2">
            <div className="text-[11px] uppercase tracking-wide text-muted-foreground">
              {t(($) => $.review_lens.gates_heading)}
            </div>
            {hasOverride && (
              <Badge variant="outline">{t(($) => $.review_lens.override_badge)}</Badge>
            )}
          </div>

          {isLoading ? (
            <p className="text-sm text-muted-foreground">{t(($) => $.timeline.loading)}</p>
          ) : !readiness || readiness.gates.length === 0 ? (
            <div className="rounded-lg border border-dashed bg-muted/20 px-3 py-5 text-center">
              <p className="text-[12px] text-muted-foreground">{t(($) => $.review_lens.empty)}</p>
            </div>
          ) : (
            <div>
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                {readiness.gates.map((g) => (
                  <GateCard key={g.name} gate={g} />
                ))}
              </div>
              <div className="mt-2 text-[11px] text-muted-foreground">
                {t(($) => $.review_lens.tier_label)}: {readiness.tier} ·{" "}
                {readiness.ready
                  ? t(($) => $.review_lens.ready)
                  : t(($) => $.review_lens.blocked)}
              </div>
            </div>
          )}
        </section>

        <section>
          <div className="mb-2 text-[11px] uppercase tracking-wide text-muted-foreground">
            {t(($) => $.detail.section_pull_requests)}
          </div>
          <div className="rounded-lg border px-2 py-1.5">
            <PullRequestList issueId={issueId} />
          </div>
        </section>
      </div>
    </div>
  );
}
