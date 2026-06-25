"use client";

import { useMemo, useState } from "react";
import { CalendarClock, ChevronRight } from "lucide-react";
import { Accordion } from "@base-ui/react/accordion";
import { cn } from "@agora/ui/lib/utils";
import type { Issue, Sprint } from "@agora/core/types";
import { formatDateOnly } from "@agora/core/issues/date";
import { SPRINT_STATUS_CONFIG } from "@agora/core/sprints/config";
import { ListRow, type ChildProgress } from "./list-row";
import { useT } from "../../i18n";
import { useSprintStatusLabels } from "../../projects/components/sprint-labels";

const EMPTY_PROGRESS_MAP = new Map<string, ChildProgress>();

// Sentinel section key for the "No sprint" bucket. Sprint ids are uuids so it
// can never collide with a real sprint id, and keeping it out of the Sprint[]
// order list lets us always render it last.
const NO_SPRINT = "__no_sprint__";

function formatSprintDates(
  sprint: Sprint,
  noDates: string,
  range: (start: string, end: string) => string,
): string {
  if (!sprint.start_date && !sprint.end_date) return noDates;
  const fmt = (d: string | null) =>
    d ? formatDateOnly(d, { month: "short", day: "numeric" }, "en-US") : "…";
  return range(fmt(sprint.start_date), fmt(sprint.end_date));
}

/**
 * Sprint tree view — issues grouped under their sprint as collapsible
 * sections, with a trailing "No sprint" bucket for unassigned issues. Only
 * rendered on the project detail surface when sprint mode is on (gated by the
 * `tree` view option, which is itself opt-in via `allowTree`).
 *
 * Mirrors the Accordion + sticky-header styling of `ListView`'s status
 * sections and reuses `ListRow` so rows look identical to the list view.
 * Unlike the list view this has no drag/infinite-scroll: the project issue
 * set is already fully in memory (filtered upstream), so each section just
 * maps its issues to a `ListRow`. Section order follows the sprint list, then
 * the "No sprint" bucket last. Sections with no issues are omitted.
 */
export function SprintTreeView({
  issues,
  sprints,
  childProgressMap = EMPTY_PROGRESS_MAP,
  projectId: _projectId,
}: {
  issues: Issue[];
  sprints: Sprint[];
  childProgressMap?: Map<string, ChildProgress>;
  projectId: string;
}) {
  const { t } = useT("projects");
  const statusLabels = useSprintStatusLabels();

  // Bucket issues by sprint id. `sprint_id` is optional/null when an issue
  // belongs to no sprint — those land in the NO_SPRINT bucket.
  const issuesBySprint = useMemo(() => {
    const map = new Map<string, Issue[]>();
    for (const issue of issues) {
      const key = issue.sprint_id ?? NO_SPRINT;
      const bucket = map.get(key);
      if (bucket) bucket.push(issue);
      else map.set(key, [issue]);
    }
    return map;
  }, [issues]);

  // Sections to render: sprints (in list order) that have issues, then the
  // "No sprint" bucket if it has any. Sprint id is the Accordion item value.
  const sections = useMemo(() => {
    const out: { key: string; sprint: Sprint | null; issues: Issue[] }[] = [];
    for (const sprint of sprints) {
      const sprintIssues = issuesBySprint.get(sprint.id);
      if (sprintIssues && sprintIssues.length > 0) {
        out.push({ key: sprint.id, sprint, issues: sprintIssues });
      }
    }
    const noSprintIssues = issuesBySprint.get(NO_SPRINT);
    if (noSprintIssues && noSprintIssues.length > 0) {
      out.push({ key: NO_SPRINT, sprint: null, issues: noSprintIssues });
    }
    return out;
  }, [sprints, issuesBySprint]);

  // Local collapse state — default every section open. There's no persisted
  // per-sprint collapse in the view store (status collapse is status-keyed),
  // so this stays component-local. Seeded once from the initial sections.
  const [collapsedKeys, setCollapsedKeys] = useState<Set<string>>(
    () => new Set(),
  );
  const expandedKeys = useMemo(
    () => sections.map((s) => s.key).filter((k) => !collapsedKeys.has(k)),
    [sections, collapsedKeys],
  );

  // Parent owns the empty state — render nothing when there are no issues.
  if (issues.length === 0) return null;

  return (
    <div className="flex-1 min-h-0 overflow-y-auto p-2 pt-0">
      <Accordion.Root
        multiple
        className="space-y-1"
        value={expandedKeys}
        onValueChange={(value: string[]) => {
          setCollapsedKeys(() => {
            const next = new Set<string>();
            for (const section of sections) {
              if (!value.includes(section.key)) next.add(section.key);
            }
            return next;
          });
        }}
      >
        {sections.map(({ key, sprint, issues: sectionIssues }) => {
          const cfg = sprint ? SPRINT_STATUS_CONFIG[sprint.status] : null;
          // "Done" mirrors the child-progress convention used elsewhere
          // (see issue-detail): done + cancelled both count as resolved.
          const doneCount = sectionIssues.filter(
            (i) => i.status === "done" || i.status === "cancelled",
          ).length;
          return (
            <Accordion.Item key={key} value={key}>
              <Accordion.Header className="group/header sticky top-0 z-10 flex h-10 items-center rounded-lg bg-muted transition-colors hover:bg-accent">
                <Accordion.Trigger className="group/trigger flex flex-1 items-center gap-2 px-3 h-full text-left outline-none cursor-pointer">
                  <ChevronRight className="size-3.5 shrink-0 text-muted-foreground transition-transform group-aria-expanded/trigger:rotate-90" />
                  {sprint ? (
                    <>
                      <span
                        className={cn("size-2 shrink-0 rounded-full", cfg!.dotColor)}
                        aria-hidden
                      />
                      <span className="truncate text-sm font-medium">
                        {sprint.name}
                      </span>
                      <span
                        className={cn(
                          "shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium",
                          cfg!.badgeBg,
                          cfg!.badgeText,
                        )}
                      >
                        {statusLabels[sprint.status]}
                      </span>
                      <span className="hidden shrink-0 items-center gap-1 text-[11px] text-muted-foreground sm:inline-flex">
                        <CalendarClock className="size-3 shrink-0" />
                        <span className="truncate">
                          {formatSprintDates(
                            sprint,
                            t(($) => $.sprints.no_dates),
                            (start, end) =>
                              t(($) => $.sprints.date_range, { start, end }),
                          )}
                        </span>
                      </span>
                    </>
                  ) : (
                    <>
                      <span
                        className="size-2 shrink-0 rounded-full bg-muted-foreground/40"
                        aria-hidden
                      />
                      <span className="truncate text-sm font-medium text-muted-foreground">
                        {t(($) => $.sprints.no_sprint)}
                      </span>
                    </>
                  )}
                  <span className="ml-auto shrink-0 text-xs text-muted-foreground tabular-nums">
                    {t(($) => $.sprints.done_of_total, {
                      done: doneCount,
                      total: sectionIssues.length,
                    })}
                  </span>
                </Accordion.Trigger>
              </Accordion.Header>
              <Accordion.Panel>
                {sectionIssues.map((issue) => (
                  <ListRow
                    key={issue.id}
                    issue={issue}
                    childProgress={childProgressMap.get(issue.id)}
                  />
                ))}
              </Accordion.Panel>
            </Accordion.Item>
          );
        })}
      </Accordion.Root>
    </div>
  );
}
