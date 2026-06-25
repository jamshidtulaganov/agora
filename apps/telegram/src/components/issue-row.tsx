import { ChevronRight } from "lucide-react";
import type { Issue } from "@agora/core/types";
import { StatusDot, PriorityBars } from "./issue-badges";

export function IssueRow({
  issue,
  onClick,
}: {
  issue: Issue;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-center gap-3 px-4 py-3 text-left transition-colors active:bg-accent"
    >
      <StatusDot status={issue.status} className="shrink-0" />
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm font-medium text-foreground">
          {issue.title}
        </div>
        <div className="mt-0.5 flex items-center gap-2 text-xs text-muted-foreground">
          <span className="font-mono">{issue.identifier}</span>
          <PriorityBars priority={issue.priority} />
        </div>
      </div>
      <ChevronRight className="size-4 shrink-0 text-muted-foreground/60" />
    </button>
  );
}
