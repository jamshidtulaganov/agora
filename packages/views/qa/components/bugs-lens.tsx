"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Bug, ShieldCheck, Timer } from "lucide-react";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core";
import { useWorkspacePaths } from "@agora/core/paths";
import type { Issue } from "@agora/core/types";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { useT } from "../../i18n";
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
  const { t } = useT("issues");
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
    return (
      <div className="space-y-2" aria-hidden>
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-10 w-3/4" />
      </div>
    );
  }

  // One dashed-border empty card (mirrors the Suite tab's empty state) instead
  // of three separate "Nothing here." lanes when there simply are no bugs yet.
  if (bugs.length === 0) {
    return (
      <div className="rounded-lg border border-dashed bg-muted/20 px-4 py-12 text-center">
        <Bug className="mx-auto size-6 text-muted-foreground/60" />
        <p className="mt-2 text-sm text-muted-foreground">{t(($) => $.qa_cockpit.bugs_empty_title)}</p>
        <p className="mx-auto mt-1 max-w-md text-[12px] text-muted-foreground/70">
          {t(($) => $.qa_cockpit.bugs_empty_hint)}
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-5">
      <Lane
        icon={Bug}
        iconClass="text-destructive"
        title={t(($) => $.qa_cockpit.bugs_lane_open_title)}
        subtitle={t(($) => $.qa_cockpit.bugs_lane_open_subtitle)}
        issues={lanes.open}
        href={(id) => wp.issueDetail(id)}
      />
      <Lane
        // A static Loader2 (spinner glyph that isn't actually spinning) reads as
        // a stuck/broken UI — Timer reads as "in progress" without implying motion.
        icon={Timer}
        iconClass="text-muted-foreground"
        title={t(($) => $.qa_cockpit.bugs_lane_progress_title)}
        subtitle={t(($) => $.qa_cockpit.bugs_lane_progress_subtitle)}
        issues={lanes.inProgress}
        href={(id) => wp.issueDetail(id)}
      />
      <Lane
        icon={ShieldCheck}
        iconClass="text-muted-foreground"
        title={t(($) => $.qa_cockpit.bugs_lane_fixed_title)}
        subtitle={t(($) => $.qa_cockpit.bugs_lane_fixed_subtitle)}
        issues={lanes.fixed}
        href={(id) => wp.issueDetail(id)}
      />
    </div>
  );
}
