import { useRef, useState, type TouchEvent } from "react";
import { Check, UserPlus } from "lucide-react";
import type { Issue } from "@agora/core/types";
import { StatusDot, PriorityBars } from "./issue-badges";
import { Avatar } from "./avatar";
import { haptic } from "../telegram/sdk";
import { useT } from "../i18n";
import { cn } from "../lib/cn";

export type RowAssignee = { name: string; isAgent: boolean } | null;

const ACTION_W = 76; // px per swipe action

// An Agora-styled, swipeable issue row: status glyph · identifier · title …
// priority · assignee. Swipe left to reveal quick actions (Done / Assign-to-me).
export function IssueRow({
  issue,
  assignee,
  onClick,
  onDone,
  onAssignMe,
  showAssignMe,
}: {
  issue: Issue;
  assignee?: RowAssignee;
  onClick: () => void;
  onDone?: () => void;
  onAssignMe?: () => void;
  showAssignMe?: boolean;
}) {
  const t = useT();

  const actions: { key: string; icon: typeof Check; label: string; bg: string; run: () => void }[] = [];
  if (onDone && issue.status !== "done") {
    actions.push({ key: "done", icon: Check, label: t("row.done"), bg: "bg-success", run: onDone });
  }
  if (onAssignMe && showAssignMe) {
    actions.push({ key: "me", icon: UserPlus, label: t("row.assignMe"), bg: "bg-brand", run: onAssignMe });
  }
  const maxShift = actions.length * ACTION_W;

  const [tx, setTx] = useState(0);
  const [dragging, setDragging] = useState(false);
  const start = useRef({ x: 0, base: 0 });
  const moved = useRef(false);

  const onTouchStart = (e: TouchEvent) => {
    if (maxShift === 0) return;
    start.current = { x: e.touches[0]!.clientX, base: tx };
    moved.current = false;
    setDragging(true);
  };
  const onTouchMove = (e: TouchEvent) => {
    if (maxShift === 0) return;
    const delta = e.touches[0]!.clientX - start.current.x;
    if (Math.abs(delta) > 6) moved.current = true;
    setTx(Math.max(-maxShift, Math.min(0, start.current.base + delta)));
  };
  const onTouchEnd = () => {
    if (maxShift === 0) return;
    setDragging(false);
    setTx(tx < -maxShift / 2 ? -maxShift : 0);
  };

  const handleClick = () => {
    if (moved.current) {
      moved.current = false;
      return; // it was a swipe, not a tap
    }
    if (tx !== 0) {
      setTx(0); // first tap closes the revealed actions
      return;
    }
    onClick();
  };

  return (
    <div className="relative overflow-hidden">
      {actions.length > 0 && (
        <div className="absolute inset-y-0 right-0 flex">
          {actions.map((a) => (
            <button
              key={a.key}
              type="button"
              onClick={() => {
                haptic("medium");
                a.run();
                setTx(0);
              }}
              className={cn(
                "flex flex-col items-center justify-center gap-0.5 text-[11px] font-medium text-white",
                a.bg,
              )}
              style={{ width: ACTION_W }}
            >
              <a.icon className="size-4" />
              {a.label}
            </button>
          ))}
        </div>
      )}
      <button
        type="button"
        onClick={handleClick}
        onTouchStart={onTouchStart}
        onTouchMove={onTouchMove}
        onTouchEnd={onTouchEnd}
        style={{
          transform: `translateX(${tx}px)`,
          transition: dragging ? "none" : "transform 0.18s ease",
        }}
        className="relative flex w-full items-center gap-2.5 bg-background px-4 py-2.5 text-left active:bg-accent"
      >
        <StatusDot status={issue.status} className="shrink-0" />
        <span className="shrink-0 font-mono text-xs text-muted-foreground">{issue.identifier}</span>
        <span className="min-w-0 flex-1 truncate text-sm text-foreground">{issue.title}</span>
        <PriorityBars priority={issue.priority} className="shrink-0" />
        {assignee && (
          <Avatar name={assignee.name} isAgent={assignee.isAgent} size={22} className="shrink-0" />
        )}
      </button>
    </div>
  );
}
