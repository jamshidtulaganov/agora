"use client";

import { useQuery } from "@tanstack/react-query";
import { Rocket } from "lucide-react";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core";
import { issueDetailOptions } from "@agora/core/issues/queries";
import { useWorkspacePaths } from "@agora/core/paths";
import type { MergeGateStatus } from "@agora/core/types";
import { Badge } from "@agora/ui/components/ui/badge";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { AppLink } from "../../navigation";
import { PullRequestList } from "./pull-request-list";
import { verdictIcon, verdictTone } from "../../qa/components/verdict";

// The Review lens — a merge workbench, matching the QA/Dev/Design lenses'
// wide two-column shape (docs/design-stage-research.md's sibling brief).
// LEFT (1fr, primary): the issue's PR list — checks, conflicts, files —
// given the room a rich card deserves instead of a squeezed sidebar row.
// RIGHT (~380px): merge-readiness gates (the same deterministic gate spine
// the editor's review bar polls, see editor-gates.tsx), tier, the
// merge:override badge, and a compact deploy-readiness note pointing at the
// sprint-level deploy panel (now on the QA cockpit's Sprint view — deploy
// moved out of the issue lens, see docs/sdlc-stage-cockpit-plan.md). v1 is
// intentionally read-only: no merge/override mutation buttons here — merge
// actions stay agent-side/GitHub for now. Shares the ["merge-readiness",
// issueId] query key with EditorGates so the cache (and the 15s poll) is
// shared, not duplicated.

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
  const wp = useWorkspacePaths();
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
    <div className="flex-1 overflow-y-auto">
      <div className="w-full px-8 py-8">
        <div className="lg:grid lg:grid-cols-[minmax(0,1fr)_380px] lg:items-start lg:gap-6">
          {/* PR list — the primary content. Full width instead of a squeezed
              sidebar row, so checks/conflicts/files/stats actually breathe. */}
          <section className="order-2 min-w-0 lg:order-1">
            <div className="mb-2 text-[11px] uppercase tracking-wide text-muted-foreground">
              {t(($) => $.detail.section_pull_requests)}
            </div>
            <div className="rounded-lg border px-2 py-1.5">
              <PullRequestList issueId={issueId} />
            </div>
          </section>

          {/* Merge gates + tier + override + deploy pointer. */}
          <div className="order-1 mb-6 lg:order-2 lg:mb-0 [&>*+*]:mt-6 [&>*+*]:border-t [&>*+*]:pt-6">
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
                  <div className="grid grid-cols-2 gap-2">
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

            {/* Deploy moved to the sprint-level panel (QA cockpit's Sprint
                view) — a compact pointer instead of re-mounting that panel
                here, so this lens stays read-only merge-readiness info. */}
            <section>
              <AppLink
                href={wp.qa()}
                className="flex items-center gap-2 rounded-lg border px-3 py-2 text-[12px] text-muted-foreground transition-colors hover:border-border-strong hover:bg-accent/50 hover:text-foreground"
              >
                <Rocket className="size-3.5 shrink-0" />
                <span className="flex-1">{t(($) => $.review_lens.deploy_note)}</span>
                <span className="shrink-0 font-medium text-foreground">
                  {t(($) => $.review_lens.deploy_link)}
                </span>
              </AppLink>
            </section>
          </div>
        </div>
      </div>
    </div>
  );
}
