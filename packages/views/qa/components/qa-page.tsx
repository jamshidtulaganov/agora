"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { List, ListChecks, Bug, Gauge, Rocket, MoreHorizontal } from "lucide-react";
import { useWorkspaceId } from "@agora/core";
import { projectListOptions } from "@agora/core/projects/queries";
import { sprintReadinessOptions } from "@agora/core/qa/queries";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "@agora/ui/components/ui/select";
import { Button } from "@agora/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from "@agora/ui/components/ui/dropdown-menu";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { BreadcrumbHeader } from "../../layout/breadcrumb-header";
import { QAMetricsView } from "./qa-metrics-view";
import { QASprintReadinessView } from "./qa-sprint-readiness-view";
import { QASuiteView } from "./qa-suite-view";
import { BugsLens } from "./bugs-lens";
import { ReleaseQueue, ViewToggle } from "./release-queue";
import { ReleaseHealthStrip } from "./release-health-strip";

// The Release page — the OUTER release loop: judge sprint readiness / regression
// / deploy (Ship) and reconcile QA verdicts (Queue). Per-issue QA lives in the
// issue cockpit (issue?lens=qa); this page is where the release decision is made.
//
// Two primary tabs — Ship (the decision) and Queue (daily triage) — plus a `⋯`
// overflow for the standing-maintenance surfaces (Bugs / Test suite / Metrics)
// a small team rarely needs at the top level. Ship is the default when any
// sprint is active (else Queue). The health strip on top keeps every non-Ship
// tab honest about "can we ship right now?".

type TabKey = "queue" | "ship" | "bugs" | "suite" | "metrics";

export function QAPage() {
  const wsId = useWorkspaceId();
  const { t } = useT("issues");
  const { t: tLayout } = useT("layout");
  // `null` until the default is resolved from the sprint-readiness query below;
  // a manual tab pick sets it and wins afterward.
  const [tab, setTab] = useState<TabKey | null>(null);
  const [project, setProject] = useState("all");
  // Deep-link seed from the health strip's needs-decision chip: switch to
  // Queue with the needs-human toggle pre-set. Reset right after the Queue
  // consumes it so the user can clear the toggle afterwards.
  const [needsHumanSeed, setNeedsHumanSeed] = useState(false);
  const { data: projectData } = useQuery(projectListOptions(wsId));
  const projects = projectData ?? [];
  const projectId = project !== "all" ? project : undefined;
  // Shared cache entry (the health strip + Ship view read the same factory) —
  // no extra fetch. Drives the default tab: Ship when a sprint is active, else
  // Queue. Resolve once; while it loads `tab` stays null → Queue, so the page
  // never flashes an empty Ship before falling back.
  const { data: readinessData } = useQuery(sprintReadinessOptions(wsId, projectId));
  useEffect(() => {
    if (tab !== null || !readinessData) return;
    setTab((readinessData.sprints?.length ?? 0) >= 1 ? "ship" : "queue");
  }, [tab, readinessData]);
  const effectiveTab: TabKey = tab ?? "queue";
  useEffect(() => {
    if (needsHumanSeed && effectiveTab === "queue") setNeedsHumanSeed(false);
  }, [needsHumanSeed, effectiveTab]);
  const openShip = () => setTab("ship");
  const isOverflowActive = effectiveTab === "bugs" || effectiveTab === "suite" || effectiveTab === "metrics";

  return (
    // Fill the (overflow-hidden) <main> and own our scroll: the header stays
    // pinned, the content below scrolls — otherwise a long verdict lane is
    // clipped with no way to reach the bottom rows.
    <div className="flex h-full min-h-0 w-full flex-col">
      {/* Same top bar as the issue pages — a fixed leaf (this is a root
          surface, no ancestor to crumb to) with the tab switch as the
          right-side action. */}
      <BreadcrumbHeader
        segments={[]}
        leaf={<span className="truncate font-medium text-foreground">{tLayout(($) => $.nav.qa)}</span>}
        actions={
          <div className="flex items-center gap-2">
            {/* Project scope applies to EVERY tab — hoisted here so the whole
                page follows one selector. "All projects" = workspace-wide. */}
            <Select value={project} onValueChange={(v) => setProject(v ?? "all")}>
              <SelectTrigger className="h-8 w-44 text-[13px]">
                <SelectValue>
                  {() =>
                    project === "all"
                      ? t(($) => $.qa_cockpit.all_projects)
                      : (projects.find((p) => p.id === project)?.title ?? t(($) => $.qa_cockpit.project_fallback))
                  }
                </SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">{t(($) => $.qa_cockpit.all_projects)}</SelectItem>
                {projects.map((p) => (
                  <SelectItem key={p.id} value={p.id}>
                    {p.title}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {/* Ship + Queue are the release loop; the `⋯` overflow holds the
                standing maintenance surfaces (Bugs / Test suite / Metrics). */}
            <div className="flex items-center gap-1 rounded-md border p-0.5">
              <ViewToggle active={effectiveTab === "ship"} onClick={openShip} icon={Rocket} label={t(($) => $.qa_cockpit.view_ship)} />
              <ViewToggle active={effectiveTab === "queue"} onClick={() => setTab("queue")} icon={List} label={t(($) => $.qa_cockpit.view_queue)} />
              <div className="mx-0.5 w-px self-stretch bg-border" aria-hidden />
              <DropdownMenu>
                <DropdownMenuTrigger
                  render={
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      aria-label={t(($) => $.qa_cockpit.view_more)}
                      className={cn(
                        "h-7 gap-1 px-2 text-[12px]",
                        isOverflowActive
                          ? "bg-muted font-medium text-foreground"
                          : "text-muted-foreground hover:text-foreground",
                      )}
                    >
                      <MoreHorizontal className="size-3.5" />
                    </Button>
                  }
                />
                <DropdownMenuContent align="end">
                  <DropdownMenuItem onClick={() => setTab("bugs")}>
                    <Bug className="size-3.5" />
                    {t(($) => $.qa_cockpit.view_bugs)}
                  </DropdownMenuItem>
                  <DropdownMenuItem onClick={() => setTab("suite")}>
                    <ListChecks className="size-3.5" />
                    {t(($) => $.qa_cockpit.view_suite)}
                  </DropdownMenuItem>
                  <DropdownMenuItem onClick={() => setTab("metrics")}>
                    <Gauge className="size-3.5" />
                    {t(($) => $.qa_cockpit.view_metrics)}
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          </div>
        }
      />

      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        {/* The strip repeats what Ship already shows — every tab EXCEPT ship. */}
        {effectiveTab !== "ship" && (
          <ReleaseHealthStrip
            projectId={projectId}
            onOpenShip={openShip}
            onOpenQueueNeedsHuman={() => {
              setNeedsHumanSeed(true);
              setTab("queue");
            }}
          />
        )}
        {/* The Queue stays MOUNTED across tab switches (display-toggled, not
            unmounted) so an in-progress triage cut — filters, bulk selection,
            list/board layout — survives a glance at Ship/Bugs/Suite/Metrics
            instead of resetting on every return. */}
        <div className={effectiveTab === "queue" ? "contents" : "hidden"}>
          <ReleaseQueue projectId={projectId} initialNeedsHumanOnly={needsHumanSeed} onOpenShip={openShip} />
        </div>
        {effectiveTab === "ship" ? (
          <QASprintReadinessView
            projectId={projectId}
            onSeeBlockers={() => {
              setNeedsHumanSeed(true);
              setTab("queue");
            }}
          />
        ) : effectiveTab === "bugs" ? (
          <div className="flex w-full flex-col gap-4 px-8 py-8">
            <BugsLens projectId={projectId} />
          </div>
        ) : effectiveTab === "suite" ? (
          <QASuiteView projectId={projectId} />
        ) : effectiveTab === "metrics" ? (
          <QAMetricsView projectId={projectId} />
        ) : null}
      </div>
    </div>
  );
}
