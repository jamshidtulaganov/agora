import { STATUS_CONFIG, PRIORITY_CONFIG } from "@agora/core/issues/config";
import type { IssueStatus, IssuePriority } from "@agora/core/types";
import { cn } from "../lib/cn";

// Status dot + label, reusing the main app's STATUS_CONFIG colors so the Mini
// App reads identically to web/desktop.
export function StatusDot({
  status,
  className,
}: {
  status: IssueStatus;
  className?: string;
}) {
  const cfg = STATUS_CONFIG[status];
  return (
    <span
      className={cn("inline-block size-2.5 rounded-full", cfg.dividerColor, className)}
      aria-label={cfg.label}
    />
  );
}

export function StatusLabel({ status }: { status: IssueStatus }) {
  const cfg = STATUS_CONFIG[status];
  return (
    <span className="inline-flex items-center gap-1.5 text-sm">
      <StatusDot status={status} />
      <span className={cfg.iconColor}>{cfg.label}</span>
    </span>
  );
}

// Priority bars (0–4) mirroring the web priority glyph.
export function PriorityBars({ priority }: { priority: IssuePriority }) {
  const cfg = PRIORITY_CONFIG[priority];
  if (cfg.bars === 0) return null;
  return (
    <span className={cn("inline-flex items-end gap-0.5", cfg.color)} aria-label={cfg.label}>
      {[1, 2, 3, 4].map((b) => (
        <span
          key={b}
          className={cn(
            "w-0.5 rounded-sm bg-current",
            b <= cfg.bars ? "opacity-100" : "opacity-25",
          )}
          style={{ height: `${3 + b * 2}px` }}
        />
      ))}
    </span>
  );
}
