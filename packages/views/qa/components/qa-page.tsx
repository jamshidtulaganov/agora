"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { List, ListChecks, Bug, Gauge, Rocket } from "lucide-react";
import { useWorkspaceId } from "@agora/core";
import { projectListOptions } from "@agora/core/projects/queries";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "@agora/ui/components/ui/select";
import { useT } from "../../i18n";
import { BreadcrumbHeader } from "../../layout/breadcrumb-header";
import { QAMetricsView } from "./qa-metrics-view";
import { QASprintReadinessView } from "./qa-sprint-readiness-view";
import { QASuiteView } from "./qa-suite-view";
import { BugsLens } from "./bugs-lens";
import { ReleaseQueue, ViewToggle } from "./release-queue";
import { ReleaseHealthStrip } from "./release-health-strip";

// The Release page — the OUTER release loop: reconcile QA verdicts (Queue),
// judge sprint readiness / regression / deploy (Ship), and keep the standing
// gates healthy (Bugs / Suite / Metrics). Per-issue QA lives in the issue
// cockpit (issue?lens=qa); this page is where the release decision is made.
//
// Two primary tabs (Queue = daily triage, Ship = the decision) and three
// maintenance tabs behind a divider. The health strip on top keeps every
// non-Ship tab honest about "can we ship right now?".

type TabKey = "queue" | "ship" | "bugs" | "suite" | "metrics";

export function QAPage() {
  const wsId = useWorkspaceId();
  const { t } = useT("issues");
  const { t: tLayout } = useT("layout");
  const [tab, setTab] = useState<TabKey>("queue");
  const [project, setProject] = useState("all");
  // Deep-link seed from the health strip's needs-decision chip: switch to
  // Queue with the needs-human toggle pre-set. Reset right after the Queue
  // consumes it so the user can clear the toggle afterwards.
  const [needsHumanSeed, setNeedsHumanSeed] = useState(false);
  useEffect(() => {
    if (needsHumanSeed && tab === "queue") setNeedsHumanSeed(false);
  }, [needsHumanSeed, tab]);
  const { data: projectData } = useQuery(projectListOptions(wsId));
  const projects = projectData ?? [];
  const projectId = project !== "all" ? project : undefined;
  const openShip = () => setTab("ship");

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
            {/* Queue + Ship are the release loop; the divider separates the
                standing maintenance surfaces (Bugs / Suite / Metrics). */}
            <div className="flex items-center gap-1 rounded-md border p-0.5">
              <ViewToggle active={tab === "queue"} onClick={() => setTab("queue")} icon={List} label={t(($) => $.qa_cockpit.view_queue)} />
              <ViewToggle active={tab === "ship"} onClick={openShip} icon={Rocket} label={t(($) => $.qa_cockpit.view_ship)} />
              <div className="mx-0.5 w-px self-stretch bg-border" aria-hidden />
              <ViewToggle active={tab === "bugs"} onClick={() => setTab("bugs")} icon={Bug} label={t(($) => $.qa_cockpit.view_bugs)} />
              <ViewToggle active={tab === "suite"} onClick={() => setTab("suite")} icon={ListChecks} label={t(($) => $.qa_cockpit.view_suite)} />
              <ViewToggle active={tab === "metrics"} onClick={() => setTab("metrics")} icon={Gauge} label={t(($) => $.qa_cockpit.view_metrics)} />
            </div>
          </div>
        }
      />

      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        {/* The strip repeats what Ship already shows — every tab EXCEPT ship. */}
        {tab !== "ship" && (
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
        <div className={tab === "queue" ? "contents" : "hidden"}>
          <ReleaseQueue projectId={projectId} initialNeedsHumanOnly={needsHumanSeed} onOpenShip={openShip} />
        </div>
        {tab === "ship" ? (
          <QASprintReadinessView
            projectId={projectId}
            onSeeBlockers={() => {
              setNeedsHumanSeed(true);
              setTab("queue");
            }}
          />
        ) : tab === "bugs" ? (
          <div className="flex w-full flex-col gap-4 px-8 py-8">
            <BugsLens projectId={projectId} />
          </div>
        ) : tab === "suite" ? (
          <QASuiteView projectId={projectId} />
        ) : tab === "metrics" ? (
          <QAMetricsView projectId={projectId} />
        ) : null}
      </div>
    </div>
  );
}
