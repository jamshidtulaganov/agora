import { Bot } from "lucide-react";
import { cn } from "../lib/cn";

// Agent identity per the design: violet disc + bot glyph, optional status
// dot pinned bottom-right (success = running, warning = paused/gated,
// muted = idle/offline). Violet utilities follow the existing precedent in
// packages/views (design-proposal-section) — there is no --agent token yet.

export type AgentStatusTone = "running" | "paused" | "idle" | null;

const DOT_BG: Record<Exclude<AgentStatusTone, null>, string> = {
  running: "bg-success",
  paused: "bg-warning",
  idle: "bg-muted-foreground/50",
};

export function AgentAvatar({
  size = 28,
  status = null,
  className,
}: {
  size?: number;
  status?: AgentStatusTone;
  className?: string;
}) {
  const dotSize = size >= 44 ? 13 : size >= 38 ? 12 : 10;
  return (
    <span
      className={cn(
        "relative inline-flex shrink-0 items-center justify-center rounded-full bg-violet-500/15 text-violet-700 dark:bg-violet-400/20 dark:text-violet-300",
        className,
      )}
      style={{ width: size, height: size }}
    >
      <Bot style={{ width: size * 0.54, height: size * 0.54 }} />
      {status && (
        <span
          className={cn(
            "absolute -bottom-px -right-px rounded-full border-2 border-card",
            DOT_BG[status],
          )}
          style={{ width: dotSize, height: dotSize }}
        />
      )}
    </span>
  );
}

// LEAD/role tag shown next to agent names.
export function AgentTag({ label }: { label: string }) {
  return (
    <span className="rounded-[5px] bg-violet-500/10 px-[7px] py-0.5 text-[10px] font-bold uppercase tracking-[0.04em] text-violet-700 dark:bg-violet-400/15 dark:text-violet-300">
      {label}
    </span>
  );
}
