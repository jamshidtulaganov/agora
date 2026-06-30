"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { ShieldAlert, ShieldCheck, ShieldQuestion } from "lucide-react";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core";
import { useWorkspacePaths } from "@agora/core/paths";
import type { Issue } from "@agora/core/types";
import { cn } from "@agora/ui/lib/utils";
import { AppLink } from "../../navigation";

// QA cockpit — the QA team's triage view. The in_review queue (every project)
// grouped by QA verdict so the team sees, at a glance, what needs them.
//
// A qa:fail task's outcome (the team's rule): hotfix it in the same sprint —
// which on the next in_review re-fires the auto-QA (the prev!=in_review guard
// means it re-runs) — OR move it to the next sprint. Both are done from the
// issue itself; this view is the queue + the verdict.

type QAStatus = "fail" | "pending" | "pass";

function qaStatusOf(issue: Issue): QAStatus {
  const names = (issue.labels ?? []).map((l) => l.name);
  if (names.includes("qa:fail")) return "fail";
  if (names.includes("qa:pass")) return "pass";
  return "pending";
}

export function QAPage() {
  const wsId = useWorkspaceId();
  const wp = useWorkspacePaths();
  const { data, isLoading } = useQuery({
    queryKey: ["qa-cockpit", wsId],
    queryFn: () => api.listIssues({ status: "in_review", limit: 200 }),
    staleTime: 15_000,
  });

  const issues = data?.issues ?? [];
  const lanes = useMemo(() => {
    const fail: Issue[] = [];
    const pending: Issue[] = [];
    const pass: Issue[] = [];
    for (const i of issues) {
      const s = qaStatusOf(i);
      (s === "fail" ? fail : s === "pass" ? pass : pending).push(i);
    }
    return { fail, pending, pass };
  }, [issues]);

  return (
    <div className="mx-auto flex w-full max-w-3xl flex-col gap-6 px-6 py-8">
      <header className="space-y-1">
        <h1 className="text-lg font-semibold">QA</h1>
        <p className="text-sm text-muted-foreground">
          The review queue across all projects, by QA verdict. {issues.length} in review
          {" · "}
          <span className="text-destructive">{lanes.fail.length} need fix</span>
          {" · "}
          {lanes.pending.length} pending · {lanes.pass.length} passed
        </p>
      </header>

      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : (
        <div className="space-y-5">
          <Lane
            icon={ShieldAlert}
            iconClass="text-destructive"
            title="Needs fix"
            subtitle="qa:fail — hotfix in this sprint (re-QA runs on re-review) or move to the next sprint"
            issues={lanes.fail}
            issueHref={(id) => wp.issueDetail(id)}
          />
          <Lane
            icon={ShieldQuestion}
            iconClass="text-muted-foreground"
            title="Pending QA"
            subtitle="in review — awaiting or running QA"
            issues={lanes.pending}
            issueHref={(id) => wp.issueDetail(id)}
          />
          <Lane
            icon={ShieldCheck}
            iconClass="text-muted-foreground"
            title="Passed"
            subtitle="qa:pass — ready to merge"
            issues={lanes.pass}
            issueHref={(id) => wp.issueDetail(id)}
          />
        </div>
      )}
    </div>
  );
}

function Lane({
  icon: Icon,
  iconClass,
  title,
  subtitle,
  issues,
  issueHref,
}: {
  icon: typeof ShieldAlert;
  iconClass: string;
  title: string;
  subtitle: string;
  issues: Issue[];
  issueHref: (id: string) => string;
}) {
  return (
    <section className="rounded-lg border">
      <div className="flex items-center gap-2 border-b px-3 py-2">
        <Icon className={cn("size-4 shrink-0", iconClass)} />
        <span className="text-sm font-medium">{title}</span>
        <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
          {issues.length}
        </span>
        <span className="ml-2 truncate text-[11px] text-muted-foreground">{subtitle}</span>
      </div>
      {issues.length === 0 ? (
        <p className="px-3 py-2 text-[12px] text-muted-foreground">Nothing here.</p>
      ) : (
        <ul className="divide-y">
          {issues.map((issue) => (
            <li key={issue.id}>
              <AppLink
                href={issueHref(issue.id)}
                className="flex items-center gap-2 px-3 py-1.5 text-sm hover:bg-accent/60"
              >
                <span className="truncate">{issue.title}</span>
              </AppLink>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
