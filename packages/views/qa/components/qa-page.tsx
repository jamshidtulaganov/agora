"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ShieldAlert,
  ShieldCheck,
  ShieldQuestion,
  List,
  LayoutGrid,
  Bug,
  ListFilter,
  User,
  X,
} from "lucide-react";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core";
import { useWorkspacePaths } from "@agora/core/paths";
import { projectListOptions } from "@agora/core/projects/queries";
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
type ViewMode = "list" | "board" | "bugs";
type AssigneeKey = `${string}:${string}`;

function qaStatusOf(issue: Issue): QAStatus {
  const names = (issue.labels ?? []).map((l) => l.name);
  if (names.includes("qa:fail")) return "fail";
  if (names.includes("qa:pass")) return "pass";
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
  const { data, isLoading } = useQuery({
    queryKey: ["qa-cockpit", wsId, project],
    queryFn: () =>
      api.listIssues({ status: "in_review", limit: 200, ...(project !== "all" ? { project_id: project } : {}) }),
    staleTime: 15_000,
  });

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
    return by;
  }, [filteredIssues]);

  const hasFilters = assigneeFilter.length > 0 || priorityFilter.length > 0;

  return (
    <div className="flex w-full flex-col">
      {/* Same top bar as the issue / QA review pages — a fixed leaf (this is
          a root surface, no ancestor to crumb to) with the view switch as the
          right-side action. */}
      <BreadcrumbHeader
        segments={[]}
        leaf={<span className="truncate font-medium text-foreground">QA</span>}
        actions={
          <div className="flex items-center gap-1 rounded-md border p-0.5">
            <ViewToggle active={view === "list"} onClick={() => setView("list")} icon={List} label="List" />
            <ViewToggle active={view === "board"} onClick={() => setView("board")} icon={LayoutGrid} label="Board" />
            <ViewToggle active={view === "bugs"} onClick={() => setView("bugs")} icon={Bug} label="Bugs" />
          </div>
        }
      />

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
            {" · "}
            {lanes.pending.length} pending · {lanes.pass.length} passed
          </p>

          {view !== "bugs" && (
            <div className="flex flex-wrap items-center gap-2">
              <Select value={project} onValueChange={(v) => setProject(v ?? "all")}>
                <SelectTrigger className="h-8 w-48 text-[13px]">
                  <SelectValue />
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
        <BugsLens />
      ) : isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : view === "list" ? (
        <div className="space-y-5">
          {LANES.map(({ key, ...lane }) => (
            <Lane key={key} {...lane} issues={lanes[key]} href={wp.qaDetail} />
          ))}
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
          {LANES.map(({ key, ...lane }) => (
            <BoardColumn key={key} {...lane} issues={lanes[key]} href={wp.qaDetail} />
          ))}
        </div>
      )}
      </div>
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
}: {
  icon: typeof ShieldAlert;
  iconClass: string;
  title: string;
  subtitle: string;
  issues: Issue[];
  href: (id: string) => string;
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
              <QAIssueRow issue={issue} />
            </AppLink>
          ))
        )}
      </div>
    </section>
  );
}
