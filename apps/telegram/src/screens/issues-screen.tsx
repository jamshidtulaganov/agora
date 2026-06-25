import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { issueListOptions } from "@agora/core/issues/queries";
import { STATUS_CONFIG, BOARD_STATUSES } from "@agora/core/issues/config";
import { useWorkspaceId } from "@agora/core/hooks";
import type { Issue, IssueStatus } from "@agora/core/types";
import { useRouter } from "../platform/navigation";
import { IssueRow } from "../components/issue-row";
import { CenterMessage } from "../components/center-message";
import { cn } from "../lib/cn";

type Filter = "all" | "active";

// Statuses considered "active" work (the default triage focus).
const ACTIVE_STATUSES: IssueStatus[] = ["todo", "in_progress", "in_review", "blocked"];

export function IssuesScreen() {
  const wsId = useWorkspaceId();
  const { data: issues = [], isLoading } = useQuery(issueListOptions(wsId));
  const { navigate } = useRouter();
  const [filter, setFilter] = useState<Filter>("active");

  const groups = useMemo(() => {
    const visible = issues.filter((i) =>
      filter === "active" ? ACTIVE_STATUSES.includes(i.status) : true,
    );
    return BOARD_STATUSES.map((status) => ({
      status,
      issues: visible.filter((i) => i.status === status),
    })).filter((g) => g.issues.length > 0);
  }, [issues, filter]);

  if (isLoading) return <CenterMessage spinner title="Loading issues…" />;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 gap-2 border-b border-border px-3 py-2">
        {(["active", "all"] as Filter[]).map((f) => (
          <button
            key={f}
            type="button"
            onClick={() => setFilter(f)}
            className={cn(
              "rounded-full px-3 py-1 text-xs font-medium capitalize transition-colors",
              filter === f
                ? "bg-foreground text-background"
                : "bg-muted text-muted-foreground",
            )}
          >
            {f}
          </button>
        ))}
      </div>

      {groups.length === 0 ? (
        <CenterMessage title="No issues" subtitle="Tap “New” to create one." />
      ) : (
        <div className="flex-1 overflow-y-auto">
          {groups.map((g) => (
            <StatusGroup
              key={g.status}
              status={g.status}
              issues={g.issues}
              onOpen={(id) => navigate({ name: "issue", id })}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function StatusGroup({
  status,
  issues,
  onOpen,
}: {
  status: IssueStatus;
  issues: Issue[];
  onOpen: (id: string) => void;
}) {
  const cfg = STATUS_CONFIG[status];
  return (
    <section>
      <div className="sticky top-0 z-10 flex items-center gap-2 bg-muted/80 px-4 py-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground backdrop-blur">
        <span className={cn("size-2 rounded-full", cfg.dividerColor)} />
        {cfg.label}
        <span className="font-normal text-muted-foreground/70">{issues.length}</span>
      </div>
      <ul className="divide-y divide-border">
        {issues.map((issue) => (
          <li key={issue.id}>
            <IssueRow issue={issue} onClick={() => onOpen(issue.id)} />
          </li>
        ))}
      </ul>
    </section>
  );
}
