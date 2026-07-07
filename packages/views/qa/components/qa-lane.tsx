import { useState } from "react";
import type { LucideIcon } from "lucide-react";
import { ChevronDown, ChevronRight, Bot, User, Timer } from "lucide-react";
import type { Issue } from "@agora/core/types";
import { cn } from "@agora/ui/lib/utils";
import { AppLink } from "../../navigation";
import { ActorAvatar } from "../../common/actor-avatar";
import { PriorityIcon } from "../../issues/components/priority-icon";

// One issue's freshest QA verdict summary (from GET /api/qa/verdicts) — lets a
// lane row answer "why is this here" without opening the issue.
export interface QAVerdictInfo {
  verdict: string;
  source: string;
  summary: string;
  captured_at: string;
}

function relAge(iso: string): string {
  if (!iso) return "";
  const ms = Date.now() - new Date(iso).getTime();
  if (!Number.isFinite(ms) || ms < 0) return "";
  const m = Math.floor(ms / 60_000);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h}h`;
  return `${Math.floor(h / 24)}d`;
}

// The identifier + priority + title + assignee row shared by the QA cockpit's
// list lanes and board cards, so both read as the same surface and carry the
// same "who owns this" signal at a glance. When a verdict summary is supplied,
// a second line answers WHY the row is in its lane (reason + provenance + age)
// — the audit's top daily-pain finding was that this required opening every
// issue one by one.
export function QAIssueRow({
  issue,
  isLive = false,
  verdictInfo,
}: {
  issue: Issue;
  isLive?: boolean;
  verdictInfo?: QAVerdictInfo;
}) {
  const stale = (issue.labels ?? []).some((l) => l.name === "qa:stale");
  return (
    <div className="min-w-0 flex-1">
      <div className="flex items-center gap-2">
        <PriorityIcon priority={issue.priority} className="shrink-0" />
        <span className="w-14 shrink-0 text-xs text-muted-foreground">{issue.identifier}</span>
        <span className="min-w-0 flex-1 truncate">{issue.title}</span>
        {stale && (
          <span
            className="flex shrink-0 items-center gap-1 rounded-full bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-amber-600 dark:text-amber-400"
            title="The QA gate never produced a verdict (agent died / never dispatched) — re-run QA"
          >
            stale
          </span>
        )}
        {/* Live QA indicator: a QA run is executing on this issue right now. */}
        {isLive && (
          <span
            className="flex shrink-0 items-center gap-1 rounded-full bg-info/10 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-info"
            title="QA is running on this issue now"
          >
            <span aria-hidden className="size-1.5 rounded-full bg-info motion-safe:animate-pulse" />
            live
          </span>
        )}
        {issue.assignee_type && issue.assignee_id && (
          <ActorAvatar actorType={issue.assignee_type} actorId={issue.assignee_id} size={20} enableHoverCard />
        )}
      </div>
      {verdictInfo?.summary && (
        <div className="mt-0.5 flex items-center gap-1.5 pl-6 text-[11px] text-muted-foreground">
          <span
            className="flex shrink-0 items-center gap-0.5 rounded border px-1 py-px text-[9px] uppercase tracking-wide"
            title={`verdict source: ${verdictInfo.source || "agent"}`}
          >
            {(verdictInfo.source || "agent") === "agent" ? (
              <Bot className="size-2.5" aria-hidden />
            ) : (
              <User className="size-2.5" aria-hidden />
            )}
            {verdictInfo.source || "agent"}
          </span>
          <span className="min-w-0 truncate" title={verdictInfo.summary}>
            {verdictInfo.summary}
          </span>
          {verdictInfo.captured_at && (
            <span className="ml-auto flex shrink-0 items-center gap-0.5" title={verdictInfo.captured_at}>
              <Timer className="size-2.5" aria-hidden />
              {relAge(verdictInfo.captured_at)}
            </span>
          )}
        </div>
      )}
    </div>
  );
}

// A QA cockpit lane — a titled, counted list of issue rows. Shared by the QA
// cockpit (verdict lanes) and the Bugs lens (bug lifecycle lanes) so both read
// as the same surface. Rows link wherever `href` points (qa review / issue).
// Optional affordances (all off unless the caller wires them):
//   selection — per-row checkboxes + selected set (the cockpit's bulk bar);
//   defaultCollapsed — drains decided lanes (e.g. Passed) out of the daily
//   triage view instead of letting them grow all sprint;
//   verdicts — the per-issue reason/provenance/age second line.
export function Lane({
  icon: Icon,
  iconClass,
  title,
  subtitle,
  issues,
  href,
  liveIssueIds,
  verdicts,
  selected,
  onToggleSelect,
  defaultCollapsed = false,
}: {
  icon: LucideIcon;
  iconClass: string;
  title: string;
  subtitle: string;
  issues: Issue[];
  href: (id: string) => string;
  liveIssueIds?: Set<string>;
  verdicts?: Record<string, QAVerdictInfo>;
  selected?: Set<string>;
  onToggleSelect?: (id: string) => void;
  defaultCollapsed?: boolean;
}) {
  const [collapsed, setCollapsed] = useState(defaultCollapsed);
  const Chevron = collapsed ? ChevronRight : ChevronDown;
  return (
    <section className="rounded-lg border">
      <button
        type="button"
        onClick={() => setCollapsed((c) => !c)}
        className="flex w-full items-center gap-2 border-b px-3 py-2 text-left"
      >
        <Chevron className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
        <Icon className={cn("size-4 shrink-0", iconClass)} />
        <span className="text-sm font-medium">{title}</span>
        <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{issues.length}</span>
        <span className="ml-2 truncate text-[11px] text-muted-foreground">{subtitle}</span>
      </button>
      {collapsed ? null : issues.length === 0 ? (
        <p className="px-3 py-2 text-[12px] text-muted-foreground">Nothing here.</p>
      ) : (
        <ul className="divide-y">
          {issues.map((issue) => (
            <li key={issue.id} className="flex items-center">
              {onToggleSelect && (
                <input
                  type="checkbox"
                  className="ml-3 size-3.5 shrink-0 accent-primary"
                  checked={selected?.has(issue.id) ?? false}
                  onChange={() => onToggleSelect(issue.id)}
                  aria-label={`select ${issue.identifier}`}
                />
              )}
              <AppLink
                href={href(issue.id)}
                className="flex min-w-0 flex-1 items-center gap-2 px-3 py-1.5 text-sm hover:bg-accent/60"
              >
                <QAIssueRow issue={issue} isLive={liveIssueIds?.has(issue.id)} verdictInfo={verdicts?.[issue.id]} />
              </AppLink>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
