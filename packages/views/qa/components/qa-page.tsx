"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ShieldAlert, ShieldCheck, ShieldQuestion, List, LayoutGrid, Bug } from "lucide-react";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core";
import { useWorkspacePaths } from "@agora/core/paths";
import type { Issue } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import { cn } from "@agora/ui/lib/utils";
import { AppLink } from "../../navigation";
import { Lane } from "./qa-lane";
import { BugsLens } from "./bugs-lens";

// QA cockpit — the QA team's triage view. The in_review queue (every project)
// grouped by QA verdict so the team sees, at a glance, what needs them. Two
// layouts: a dense list and a Kanban board (verdict columns), like the issues
// board.
//
// A qa:fail task's outcome (the team's rule): hotfix it in the same sprint —
// which on the next in_review re-fires the auto-QA (the prev!=in_review guard
// means it re-runs) — OR move it to the next sprint. Done from the QA review
// page; this view is the queue + the verdict.

type QAStatus = "fail" | "pending" | "pass";
type ViewMode = "list" | "board" | "bugs";

function qaStatusOf(issue: Issue): QAStatus {
  const names = (issue.labels ?? []).map((l) => l.name);
  if (names.includes("qa:fail")) return "fail";
  if (names.includes("qa:pass")) return "pass";
  return "pending";
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
  const { data, isLoading } = useQuery({
    queryKey: ["qa-cockpit", wsId],
    queryFn: () => api.listIssues({ status: "in_review", limit: 200 }),
    staleTime: 15_000,
  });

  const issues = data?.issues ?? [];
  const lanes = useMemo(() => {
    const by: Record<QAStatus, Issue[]> = { fail: [], pending: [], pass: [] };
    for (const i of issues) by[qaStatusOf(i)].push(i);
    return by;
  }, [issues]);

  return (
    <div className="flex w-full flex-col gap-6 px-8 py-8">
      <header className="flex items-start gap-4">
        <div className="space-y-1">
          <h1 className="text-lg font-semibold">QA</h1>
          <p className="text-sm text-muted-foreground">
            The review queue across all projects, by QA verdict. {issues.length} in review
            {" · "}
            <span className="text-destructive">{lanes.fail.length} need fix</span>
            {" · "}
            {lanes.pending.length} pending · {lanes.pass.length} passed
          </p>
        </div>
        <div className="ml-auto flex items-center gap-1 rounded-md border p-0.5">
          <ViewToggle active={view === "list"} onClick={() => setView("list")} icon={List} label="List" />
          <ViewToggle active={view === "board"} onClick={() => setView("board")} icon={LayoutGrid} label="Board" />
          <ViewToggle active={view === "bugs"} onClick={() => setView("bugs")} icon={Bug} label="Bugs" />
        </div>
      </header>

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
              className="rounded-md border bg-background px-3 py-2 text-[13px] hover:border-border-strong hover:bg-accent/50"
            >
              <span className="line-clamp-2">{issue.title}</span>
            </AppLink>
          ))
        )}
      </div>
    </section>
  );
}
