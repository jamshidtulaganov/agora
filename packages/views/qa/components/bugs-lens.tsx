"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Bug, Loader2, ShieldCheck } from "lucide-react";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core";
import { useWorkspacePaths } from "@agora/core/paths";
import type { Issue } from "@agora/core/types";
import { Lane } from "./qa-lane";

// The Bugs lens on the QA cockpit. Bugs filed from failed verdicts (the
// file-bug flow) are ordinary issues carrying the `bug` label, so they thread
// through their OWN auto-QA gate — a repro is "verified fixed" only when the bug
// itself reaches status=done with a qa:pass. Three lanes track that lifecycle.

function hasLabel(issue: Issue, name: string): boolean {
  return (issue.labels ?? []).some((l) => l.name === name);
}

export function BugsLens({ projectId }: { projectId?: string }) {
  const wsId = useWorkspaceId();
  const wp = useWorkspacePaths();
  const { data, isLoading } = useQuery({
    queryKey: ["qa-bugs", wsId, projectId ?? "all"],
    queryFn: () => api.listIssues({ limit: 200, ...(projectId ? { project_id: projectId } : {}) }),
    staleTime: 15_000,
  });

  const bugs = useMemo(() => (data?.issues ?? []).filter((i) => hasLabel(i, "bug")), [data]);
  const lanes = useMemo(() => {
    const open: Issue[] = [];
    const inProgress: Issue[] = [];
    const fixed: Issue[] = [];
    for (const b of bugs) {
      if (b.status === "done" && hasLabel(b, "qa:pass")) fixed.push(b);
      else if (b.status === "in_progress" || b.status === "in_review") inProgress.push(b);
      else open.push(b);
    }
    return { open, inProgress, fixed };
  }, [bugs]);

  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading…</p>;
  }

  return (
    <div className="space-y-5">
      <Lane
        icon={Bug}
        iconClass="text-destructive"
        title="Open"
        subtitle="filed from a failed verdict — awaiting triage"
        issues={lanes.open}
        href={(id) => wp.issueDetail(id)}
      />
      <Lane
        icon={Loader2}
        iconClass="text-muted-foreground"
        title="In progress"
        subtitle="being fixed — re-QA runs on re-review"
        issues={lanes.inProgress}
        href={(id) => wp.issueDetail(id)}
      />
      <Lane
        icon={ShieldCheck}
        iconClass="text-muted-foreground"
        title="Verified fixed"
        subtitle="done + qa:pass — the repro is closed for real"
        issues={lanes.fixed}
        href={(id) => wp.issueDetail(id)}
      />
    </div>
  );
}
