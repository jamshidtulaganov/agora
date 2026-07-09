"use client";

import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  ShieldAlert,
  ShieldCheck,
  ShieldQuestion,
  List,
  ListChecks,
  LayoutGrid,
  Bug,
  Gauge,
  Rocket,
  ListFilter,
  User,
  X,
} from "lucide-react";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core";
import { useWorkspacePaths } from "@agora/core/paths";
import { projectListOptions } from "@agora/core/projects/queries";
import { agentTaskSnapshotKeys, agentTaskSnapshotOptions } from "@agora/core/agents";
import { useActorName } from "@agora/core/workspace/hooks";
import { PRIORITY_ORDER } from "@agora/core/issues/config";
import type { Issue, IssuePriority } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import {
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
} from "@agora/ui/components/ui/select";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuCheckboxItem,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from "@agora/ui/components/ui/dropdown-menu";
import { cn } from "@agora/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { PriorityIcon } from "../../issues/components/priority-icon";
import { AppLink } from "../../navigation";
import { BreadcrumbHeader } from "../../layout/breadcrumb-header";
import { QAMetricsView } from "./qa-metrics-view";
import { QASprintReadinessView } from "./qa-sprint-readiness-view";
import { QASuiteView } from "./qa-suite-view";
import { Lane, QAIssueRow } from "./qa-lane";
import { BugsLens } from "./bugs-lens";

// QA cockpit — the QA team's triage view. The in_review queue (every project)
// grouped by QA verdict so the team sees, at a glance, what needs them. Two
// layouts: a dense list and a Kanban board (verdict columns), like the issues
// board. Filters (assignee, priority, project) narrow the queue so a QA lead
// can pull just "what does Nurlan own right now" or "what's urgent."
//
// A qa:fail task's outcome (the team's rule): hotfix it in the same sprint —
// which on the next in_review re-fires the auto-QA (the prev!=in_review guard
// means it re-runs) — OR move it to the next sprint. Done from the QA review
// page; this view is the queue + the verdict.

type QAStatus = "fail" | "pending" | "pass";
type ViewMode = "list" | "board" | "bugs" | "suite" | "sprint" | "metrics";
type AssigneeKey = `${string}:${string}`;

function qaStatusOf(issue: Issue): QAStatus {
  const names = (issue.labels ?? []).map((l) => l.name);
  const fail = names.includes("qa:fail");
  const pass = names.includes("qa:pass");
  // Both labels = a legacy sticky pair from before verdicts became
  // replace-on-write; the freshest verdict is unknowable from labels, so the
  // issue needs a re-verdict — pending, not a fail that drowns the queue.
  if (fail && pass) return "pending";
  if (fail) return "fail";
  if (pass) return "pass";
  // qa:stale / qa:blocked = the gate did not run (watchdog escalation /
  // undeployable branch) — an infra state, not a test failure.
  return "pending";
}

function assigneeKey(issue: Issue): AssigneeKey | null {
  if (!issue.assignee_type || !issue.assignee_id) return null;
  return `${issue.assignee_type}:${issue.assignee_id}`;
}

const LANES = [
  {
    key: "fail" as const,
    icon: ShieldAlert,
    iconClass: "text-destructive",
    title: "Needs fix",
    subtitle: "qa:fail — hotfix in this sprint (re-QA runs on re-review) or move to the next sprint",
  },
  {
    key: "pending" as const,
    icon: ShieldQuestion,
    iconClass: "text-muted-foreground",
    title: "Pending QA",
    subtitle: "in review — awaiting or running QA",
  },
  {
    key: "pass" as const,
    icon: ShieldCheck,
    iconClass: "text-muted-foreground",
    title: "Passed",
    subtitle: "qa:pass — ready to merge",
  },
];

export function QAPage() {
  const wsId = useWorkspaceId();
  const wp = useWorkspacePaths();
  const [view, setView] = useState<ViewMode>("list");
  const [project, setProject] = useState("all");
  const [assigneeFilter, setAssigneeFilter] = useState<AssigneeKey[]>([]);
  const [priorityFilter, setPriorityFilter] = useState<IssuePriority[]>([]);
  const { data: projectData } = useQuery(projectListOptions(wsId));
  const projects = projectData ?? [];
  // Which issues have a QA run executing RIGHT NOW — mark those rows "live" so
  // a QA lead sees the queue moving. Any running task on an in_review issue is
  // effectively QA (the knowledge/dev noise runs on other statuses).
  const { data: taskSnapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));
  const liveIssueIds = useMemo(
    () => new Set(taskSnapshot.filter((t) => t.status === "running" && t.issue_id).map((t) => t.issue_id)),
    [taskSnapshot],
  );
  // issueId → live task id, so a running row can offer a Stop button. First
  // running task per issue wins (a gate is one task); `.id` is what the cancel
  // endpoint needs (SliceActionResponse carries no task id).
  const runningTaskByIssue = useMemo(() => {
    const map = new Map<string, string>();
    for (const t of taskSnapshot) {
      if (t.status === "running" && t.issue_id && !map.has(t.issue_id)) map.set(t.issue_id, t.id);
    }
    return map;
  }, [taskSnapshot]);
  const { data, isLoading } = useQuery({
    queryKey: ["qa-cockpit", wsId, project],
    queryFn: () =>
      api.listIssues({ status: "in_review", limit: 200, ...(project !== "all" ? { project_id: project } : {}) }),
    staleTime: 15_000,
  });
  // Freshest verdict per row (reason + provenance + age) — one batch call, so
  // the lanes answer "why is this here" without opening each issue.
  const { data: verdictData } = useQuery({
    queryKey: ["qa-verdicts", wsId, project],
    queryFn: () => api.listQAVerdicts(project !== "all" ? project : undefined),
    staleTime: 15_000,
  });
  const verdicts = verdictData?.verdicts ?? {};

  // Bulk triage selection (list view): 33 items in "Needs fix" must not mean
  // 33 page-opens — select rows, act once from the sticky bar.
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const toggleSelect = (id: string) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  const qc = useQueryClient();
  const bulkInvalidate = () => {
    setSelected(new Set());
    void qc.invalidateQueries({ queryKey: ["qa-cockpit", wsId] });
    void qc.invalidateQueries({ queryKey: ["qa-verdicts", wsId] });
  };
  const bulkRerunQA = useMutation({
    mutationFn: async (ids: string[]) => {
      for (const id of ids) await api.sliceAction(id, { kind: "run_qa" });
    },
    onSuccess: (_d, ids) => {
      toast.success(`Re-run QA fired on ${ids.length} issue${ids.length === 1 ? "" : "s"}`);
      bulkInvalidate();
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Bulk re-run failed"),
  });
  const bulkSendBack = useMutation({
    mutationFn: async (ids: string[]) => {
      for (const id of ids) await api.updateIssue(id, { status: "in_progress" });
    },
    onSuccess: (_d, ids) => {
      toast.success(`Sent ${ids.length} issue${ids.length === 1 ? "" : "s"} back to dev`);
      bulkInvalidate();
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Bulk send-back failed"),
  });
  // Stop a running gate from the queue. Invalidating the task snapshot refetches
  // it so the cancelled task drops out of "running" — clearing liveIssueIds /
  // runningTaskByIssue so the row's live badge + Stop button disappear without a
  // manual refresh; the cockpit + verdict queries refresh the row's verdict.
  const stopRun = useMutation({
    mutationFn: (taskId: string) => api.cancelTaskById(taskId),
    onSuccess: () => {
      toast.success("Stopping the run…");
      void qc.invalidateQueries({ queryKey: agentTaskSnapshotKeys.list(wsId) });
      void qc.invalidateQueries({ queryKey: ["qa-cockpit", wsId] });
      void qc.invalidateQueries({ queryKey: ["qa-verdicts", wsId] });
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Stop failed"),
  });
  const stoppingTaskId = stopRun.isPending ? (stopRun.variables ?? null) : null;

  const issues = data?.issues ?? [];

  const filteredIssues = useMemo(() => {
    return issues.filter((issue) => {
      if (assigneeFilter.length > 0) {
        const key = assigneeKey(issue);
        if (!key || !assigneeFilter.includes(key)) return false;
      }
      if (priorityFilter.length > 0 && !priorityFilter.includes(issue.priority)) return false;
      return true;
    });
  }, [issues, assigneeFilter, priorityFilter]);

  const lanes = useMemo(() => {
    const by: Record<QAStatus, Issue[]> = { fail: [], pending: [], pass: [] };
    for (const i of filteredIssues) by[qaStatusOf(i)].push(i);
    // Actionable-now ordering for the long "Pending" tail: a running gate and a
    // watchdog-escalated stale issue need eyes before the quiet queued rows.
    // Fail is already all-actionable; Passed is decided — only Pending reorders.
    const rank = (i: Issue) => {
      if (liveIssueIds.has(i.id)) return 0; // running now
      if ((i.labels ?? []).some((l) => l.name === "qa:stale")) return 1; // stalled gate
      return 2;
    };
    by.pending.sort((a, b) => rank(a) - rank(b));
    return by;
  }, [filteredIssues, liveIssueIds]);

  // Attention counts for the summary line — surfaced ahead of the quiet queue so
  // a QA lead reads "what needs me" first (derived from the same live set +
  // labels the rows use; no new backend field).
  const runningCount = useMemo(
    () => filteredIssues.filter((i) => liveIssueIds.has(i.id)).length,
    [filteredIssues, liveIssueIds],
  );
  const staleCount = useMemo(
    () =>
      filteredIssues.filter(
        (i) => !liveIssueIds.has(i.id) && (i.labels ?? []).some((l) => l.name === "qa:stale"),
      ).length,
    [filteredIssues, liveIssueIds],
  );

  const hasFilters = assigneeFilter.length > 0 || priorityFilter.length > 0;

  // The QA lens lives inside the issue cockpit now (docs/sdlc-stage-cockpit-plan.md
  // phase D) — there is no more dedicated /qa/<id> page, so queue rows deep-link
  // straight to the issue with the QA stage pre-selected.
  const qaLensHref = (id: string) => `${wp.issueDetail(id)}?lens=qa`;

  return (
    // Fill the (overflow-hidden) <main> and own our scroll: the header stays
    // pinned, the content below scrolls — otherwise a long verdict lane is
    // clipped with no way to reach the bottom rows.
    <div className="flex h-full min-h-0 w-full flex-col">
      {/* Same top bar as the issue / QA review pages — a fixed leaf (this is
          a root surface, no ancestor to crumb to) with the view switch as the
          right-side action. */}
      <BreadcrumbHeader
        segments={[]}
        leaf={<span className="truncate font-medium text-foreground">QA</span>}
        actions={
          <div className="flex items-center gap-2">
            {/* Project scope applies to EVERY tab (List/Board/Bugs/Sprint/
                Metrics) — hoisted here so the whole cockpit follows one
                selector, not just the list. "All projects" = workspace-wide. */}
            <Select value={project} onValueChange={(v) => setProject(v ?? "all")}>
              <SelectTrigger className="h-8 w-44 text-[13px]">
                <SelectValue>
                  {() =>
                    project === "all"
                      ? "All projects"
                      : (projects.find((p) => p.id === project)?.title ?? "Project")
                  }
                </SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All projects</SelectItem>
                {projects.map((p) => (
                  <SelectItem key={p.id} value={p.id}>
                    {p.title}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <div className="flex items-center gap-1 rounded-md border p-0.5">
              <ViewToggle active={view === "list"} onClick={() => setView("list")} icon={List} label="List" />
              <ViewToggle active={view === "board"} onClick={() => setView("board")} icon={LayoutGrid} label="Board" />
              <ViewToggle active={view === "bugs"} onClick={() => setView("bugs")} icon={Bug} label="Bugs" />
              <ViewToggle active={view === "suite"} onClick={() => setView("suite")} icon={ListChecks} label="Suite" />
              <ViewToggle active={view === "sprint"} onClick={() => setView("sprint")} icon={Rocket} label="Sprint" />
              <ViewToggle active={view === "metrics"} onClick={() => setView("metrics")} icon={Gauge} label="Metrics" />
            </div>
          </div>
        }
      />

      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
      {view === "metrics" ? (
        <QAMetricsView projectId={project !== "all" ? project : undefined} />
      ) : view === "suite" ? (
        <QASuiteView projectId={project !== "all" ? project : undefined} />
      ) : view === "sprint" ? (
        <QASprintReadinessView projectId={project !== "all" ? project : undefined} />
      ) : (
      <div className="flex w-full flex-col gap-4 px-8 py-6">
        <div className="space-y-3">
          <p className="text-sm text-muted-foreground">
            The review queue
            {project === "all"
              ? " across all projects"
              : ` for ${projects.find((p) => p.id === project)?.title ?? "this project"}`}
            , by QA verdict. {filteredIssues.length} in review
            {hasFilters && issues.length !== filteredIssues.length ? ` (of ${issues.length})` : ""}
            {" · "}
            <span className="text-destructive">{lanes.fail.length} need fix</span>
            {runningCount > 0 && (
              <>
                {" · "}
                <span className="text-info">{runningCount} running</span>
              </>
            )}
            {staleCount > 0 && (
              <>
                {" · "}
                <span className="text-amber-600 dark:text-amber-400">{staleCount} stale</span>
              </>
            )}
            {" · "}
            {lanes.pending.length} pending · {lanes.pass.length} passed
          </p>

          {view !== "bugs" && (
            <div className="flex flex-wrap items-center gap-2">
              {/* Project scope lives in the header now (applies to all tabs);
                  this row keeps the list-only assignee/priority filters. */}
              <AssigneeFilter issues={issues} selected={assigneeFilter} onChange={setAssigneeFilter} />
              <PriorityFilter issues={issues} selected={priorityFilter} onChange={setPriorityFilter} />

              {hasFilters && (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-8 gap-1 px-2 text-[12px] text-muted-foreground"
                  onClick={() => {
                    setAssigneeFilter([]);
                    setPriorityFilter([]);
                  }}
                >
                  <X className="size-3.5" />
                  Clear filters
                </Button>
              )}
            </div>
          )}
        </div>

      {view === "bugs" ? (
        <BugsLens projectId={project !== "all" ? project : undefined} />
      ) : isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : view === "list" ? (
        <div className="space-y-5">
          {LANES.map(({ key, ...lane }) => (
            <Lane
              key={key}
              {...lane}
              issues={lanes[key]}
              href={qaLensHref}
              liveIssueIds={liveIssueIds}
              verdicts={verdicts}
              selected={selected}
              onToggleSelect={toggleSelect}
              runningTaskByIssue={runningTaskByIssue}
              onStopRun={(taskId) => stopRun.mutate(taskId)}
              stoppingTaskId={stoppingTaskId}
              // Passed = already decided; collapsed by default so the triage
              // view stays about what needs a human (it grew all sprint).
              defaultCollapsed={key === "pass"}
            />
          ))}

          {selected.size > 0 && (
            <div className="sticky bottom-3 z-10 mx-auto flex w-fit items-center gap-2 rounded-full border bg-card px-4 py-2 shadow-lg">
              <span className="text-[12px] text-muted-foreground">{selected.size} selected</span>
              <Button
                size="sm"
                variant="outline"
                className="h-7 text-[11px]"
                disabled={bulkRerunQA.isPending}
                onClick={() => bulkRerunQA.mutate([...selected])}
              >
                Re-run QA
              </Button>
              <Button
                size="sm"
                variant="outline"
                className="h-7 text-[11px]"
                disabled={bulkSendBack.isPending}
                onClick={() => bulkSendBack.mutate([...selected])}
              >
                Send back to dev
              </Button>
              <Button size="sm" variant="ghost" className="h-7 text-[11px]" onClick={() => setSelected(new Set())}>
                Clear
              </Button>
            </div>
          )}
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
          {LANES.map(({ key, ...lane }) => (
            <BoardColumn
              key={key}
              {...lane}
              issues={lanes[key]}
              href={qaLensHref}
              liveIssueIds={liveIssueIds}
              verdicts={verdicts}
              runningTaskByIssue={runningTaskByIssue}
              onStopRun={(taskId) => stopRun.mutate(taskId)}
              stoppingTaskId={stoppingTaskId}
            />
          ))}
        </div>
      )}
      </div>
      )}
      </div>{/* /scroll region */}
    </div>
  );
}

function ViewToggle({
  active,
  onClick,
  icon: Icon,
  label,
}: {
  active: boolean;
  onClick: () => void;
  icon: typeof List;
  label: string;
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      onClick={onClick}
      className={cn(
        "h-7 gap-1.5 px-2 text-[12px]",
        active ? "bg-muted font-medium text-foreground" : "text-muted-foreground hover:text-foreground",
      )}
    >
      <Icon className="size-3.5" />
      {label}
    </Button>
  );
}

// Assignee options are derived from the current queue (not the full workspace
// roster) so the list only ever shows people/agents who actually have
// something in QA right now, each with a live count.
function AssigneeFilter({
  issues,
  selected,
  onChange,
}: {
  issues: Issue[];
  selected: AssigneeKey[];
  onChange: (next: AssigneeKey[]) => void;
}) {
  const { getActorName } = useActorName();
  const options = useMemo(() => {
    const counts = new Map<AssigneeKey, number>();
    for (const issue of issues) {
      const key = assigneeKey(issue);
      if (!key) continue;
      counts.set(key, (counts.get(key) ?? 0) + 1);
    }
    return Array.from(counts.entries())
      .map(([key, count]) => {
        const [actorType, actorId] = key.split(":") as [string, string];
        return { key, actorType, actorId, count, name: getActorName(actorType, actorId) };
      })
      .sort((a, b) => b.count - a.count);
  }, [issues, getActorName]);

  const toggle = (key: AssigneeKey) => {
    onChange(selected.includes(key) ? selected.filter((k) => k !== key) : [...selected, key]);
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            type="button"
            variant="outline"
            size="sm"
            className={cn("h-8 gap-1.5 px-2 text-[12px]", selected.length > 0 && "border-primary/50 text-primary")}
          >
            <User className="size-3.5" />
            Assignee
            {selected.length > 0 && (
              <span className="rounded bg-primary/10 px-1 text-[11px] font-medium">{selected.length}</span>
            )}
          </Button>
        }
      />
      <DropdownMenuContent align="start" className="w-64">
        <DropdownMenuGroup>
          <DropdownMenuLabel className="text-[11px] text-muted-foreground">
            Filter by assignee
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          {options.length === 0 ? (
            <p className="px-2 py-3 text-[12px] text-muted-foreground">No assignees in the current queue.</p>
          ) : (
            options.map((opt) => (
              <DropdownMenuCheckboxItem
                key={opt.key}
                checked={selected.includes(opt.key)}
                onCheckedChange={() => toggle(opt.key)}
                className="gap-2"
              >
                <ActorAvatar actorType={opt.actorType} actorId={opt.actorId} size={18} />
                <span className="flex-1 truncate">{opt.name}</span>
                <span className="text-[11px] text-muted-foreground">{opt.count}</span>
              </DropdownMenuCheckboxItem>
            ))
          )}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function PriorityFilter({
  issues,
  selected,
  onChange,
}: {
  issues: Issue[];
  selected: IssuePriority[];
  onChange: (next: IssuePriority[]) => void;
}) {
  const counts = useMemo(() => {
    const by = new Map<IssuePriority, number>();
    for (const issue of issues) by.set(issue.priority, (by.get(issue.priority) ?? 0) + 1);
    return by;
  }, [issues]);

  const toggle = (p: IssuePriority) => {
    onChange(selected.includes(p) ? selected.filter((x) => x !== p) : [...selected, p]);
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            type="button"
            variant="outline"
            size="sm"
            className={cn("h-8 gap-1.5 px-2 text-[12px]", selected.length > 0 && "border-primary/50 text-primary")}
          >
            <ListFilter className="size-3.5" />
            Priority
            {selected.length > 0 && (
              <span className="rounded bg-primary/10 px-1 text-[11px] font-medium">{selected.length}</span>
            )}
          </Button>
        }
      />
      <DropdownMenuContent align="start" className="w-56">
        <DropdownMenuGroup>
          <DropdownMenuLabel className="text-[11px] text-muted-foreground">
            Filter by priority
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          {PRIORITY_ORDER.filter((p) => (counts.get(p) ?? 0) > 0).map((p) => (
            <DropdownMenuCheckboxItem
              key={p}
              checked={selected.includes(p)}
              onCheckedChange={() => toggle(p)}
              className="gap-2"
            >
              <PriorityIcon priority={p} />
              <span className="flex-1 capitalize">{p === "none" ? "No priority" : p}</span>
              <span className="text-[11px] text-muted-foreground">{counts.get(p)}</span>
            </DropdownMenuCheckboxItem>
          ))}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function BoardColumn({
  icon: Icon,
  iconClass,
  title,
  issues,
  href,
  liveIssueIds,
  verdicts,
  runningTaskByIssue,
  onStopRun,
  stoppingTaskId,
}: {
  icon: typeof ShieldAlert;
  iconClass: string;
  title: string;
  subtitle: string;
  issues: Issue[];
  href: (id: string) => string;
  liveIssueIds?: Set<string>;
  verdicts?: Record<string, import("./qa-lane").QAVerdictInfo>;
  runningTaskByIssue?: Map<string, string>;
  onStopRun?: (taskId: string) => void;
  stoppingTaskId?: string | null;
}) {
  return (
    <section className="flex min-h-[200px] flex-col rounded-lg border bg-muted/20">
      <div className="flex items-center gap-2 border-b px-3 py-2">
        <Icon className={cn("size-4 shrink-0", iconClass)} />
        <span className="text-sm font-medium">{title}</span>
        <span className="ml-auto rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
          {issues.length}
        </span>
      </div>
      <div className="flex flex-col gap-2 p-2">
        {issues.length === 0 ? (
          <p className="px-1 py-2 text-[12px] text-muted-foreground">Nothing here.</p>
        ) : (
          issues.map((issue) => (
            <AppLink
              key={issue.id}
              href={href(issue.id)}
              className="flex items-center gap-2 rounded-md border bg-background px-3 py-2 text-[13px] hover:border-border-strong hover:bg-accent/50"
            >
              <QAIssueRow
                issue={issue}
                isLive={liveIssueIds?.has(issue.id)}
                verdictInfo={verdicts?.[issue.id]}
                runningTaskId={runningTaskByIssue?.get(issue.id) ?? null}
                onStopRun={onStopRun}
                stopping={!!stoppingTaskId && stoppingTaskId === (runningTaskByIssue?.get(issue.id) ?? null)}
              />
            </AppLink>
          ))
        )}
      </div>
    </section>
  );
}
