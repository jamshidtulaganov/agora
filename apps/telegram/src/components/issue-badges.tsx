import { STATUS_CONFIG, PRIORITY_CONFIG } from "@agora/core/issues/config";
import { StatusIcon } from "@agora/views/issues/components/status-icon";
import { PriorityIcon } from "@agora/views/issues/components/priority-icon";
import type { IssueStatus, IssuePriority } from "@agora/core/types";
import { cn } from "../lib/cn";

// Thin wrappers over the main app's StatusIcon / PriorityIcon so the Mini App
// renders the EXACT same status circles and priority bars as web/desktop — same
// SVG glyphs, same per-status colors from STATUS_CONFIG / PRIORITY_CONFIG.

export function StatusDot({
  status,
  className,
}: {
  status: IssueStatus;
  className?: string;
}) {
  return <StatusIcon status={status} className={cn("size-4", className)} />;
}

export function StatusLabel({ status }: { status: IssueStatus }) {
  const cfg = STATUS_CONFIG[status];
  return (
    <span className="inline-flex items-center gap-1.5 text-sm">
      <StatusIcon status={status} className="size-4" />
      <span className={cfg.iconColor}>{cfg.label}</span>
    </span>
  );
}

export function PriorityBars({
  priority,
  className,
}: {
  priority: IssuePriority;
  className?: string;
}) {
  return <PriorityIcon priority={priority} className={cn("size-4", className)} />;
}

export function PriorityLabel({ priority }: { priority: IssuePriority }) {
  const cfg = PRIORITY_CONFIG[priority];
  return (
    <span className="inline-flex items-center gap-1.5 text-sm">
      <PriorityIcon priority={priority} className="size-4" />
      <span className={cfg.color}>{cfg.label}</span>
    </span>
  );
}
