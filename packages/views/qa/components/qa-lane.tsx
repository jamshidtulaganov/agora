import { useState } from "react";
import type { LucideIcon } from "lucide-react";
import { ChevronDown, ChevronRight, Bot, User, Timer, CircleStop, Loader2 } from "lucide-react";
import type { Issue } from "@agora/core/types";
import { cn } from "@agora/ui/lib/utils";
import { Checkbox } from "@agora/ui/components/ui/checkbox";
import { useT } from "../../i18n";
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

// The QA STATE of a row, derived only from data already loaded (the live-run set
// + the issue's qa:* labels) — no new backend field. It disambiguates the single
// old "stale" chip into the states a reviewer actually triages by:
//   running — a gate task is executing on this issue right now (isLive);
//   stale   — watchdog-escalated: the gate never produced a verdict;
//   blocked — qa:blocked: a DELIBERATE infra-blocked state the gate reported
//             (e.g. an undeployable sprint branch), not a test failure;
//   fail    — qa:fail (a real test failure);
//   pass    — qa:pass (ready to merge);
//   pending — in review, queued/awaiting QA, nothing wrong yet (also covers
//             "never touched by QA at all" and a legacy fail+pass pair —
//             this dense chip doesn't need a separate never_ran bucket the
//             way the fuller server-side reconciled enum does).
// Precedence: an active run is the freshest truth, then the explicit
// watchdog stale flag, then the deliberate blocked flag, then a real
// verdict. A legacy fail+pass pair is untrustworthy → pending, not fail
// (this exact rule is fuzz-tested — qa-lane.fuzz.test.ts — do not change it
// without updating that test's invariants).
//
// Precedence ORDER (not the pending-vs-stale MAPPING TARGET for the sticky
// pair, which is this lane's own established rule) mirrors
// service.ReconcileQAState (server/internal/service/qa_state.go) at the
// label-combination level: running > stale > blocked > fail > pass > (else).
// This lane has no per-case run data at this density (a queue of N rows), so
// it can never reach the server's richer pass_with_failing_cases — that
// distinction is reserved for the qa-lens chip, which has the case data for
// ONE issue.
export type QARowState = "running" | "stale" | "blocked" | "fail" | "pass" | "pending";

export function qaRowState(issue: Issue, isLive: boolean): QARowState {
  if (isLive) return "running";
  const names = (issue.labels ?? []).map((l) => l.name);
  if (names.includes("qa:stale")) return "stale";
  if (names.includes("qa:blocked")) return "blocked";
  const fail = names.includes("qa:fail");
  const pass = names.includes("qa:pass");
  if (fail && pass) return "pending";
  if (fail) return "fail";
  if (pass) return "pass";
  return "pending";
}

// Token-based, color-coded state chip. running=info (pulsing), stale=amber
// (warning), fail=destructive, pass=emerald (the codebase's standardized success
// tint), pending=muted — the same vocabulary the verdict lanes and review page
// already use, so a row reads the same everywhere.
function useStateBadgeMeta(): Record<QARowState, { label: string; className: string; title: string; pulse?: boolean }> {
  const { t } = useT("issues");
  return {
    running: {
      label: t(($) => $.qa_cockpit.state_running),
      className: "bg-info/10 text-info",
      title: t(($) => $.qa_cockpit.state_running_title),
      pulse: true,
    },
    stale: {
      label: t(($) => $.qa_cockpit.state_stale),
      className: "bg-amber-500/10 text-amber-600 dark:text-amber-400",
      title: t(($) => $.qa_cockpit.state_stale_title),
    },
    blocked: {
      label: t(($) => $.qa_cockpit.state_blocked),
      className: "bg-amber-500/10 text-amber-600 dark:text-amber-400",
      title: t(($) => $.qa_cockpit.state_blocked_title),
    },
    fail: {
      label: t(($) => $.qa_cockpit.state_fail),
      className: "bg-destructive/10 text-destructive",
      title: t(($) => $.qa_cockpit.state_fail_title),
    },
    pass: {
      label: t(($) => $.qa_cockpit.state_pass),
      className: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
      title: t(($) => $.qa_cockpit.state_pass_title),
    },
    pending: {
      label: t(($) => $.qa_cockpit.state_pending),
      className: "bg-muted text-muted-foreground",
      title: t(($) => $.qa_cockpit.state_pending_title),
    },
  };
}

function QAStateBadge({ state }: { state: QARowState }) {
  const meta = useStateBadgeMeta();
  const b = meta[state];
  return (
    <span
      className={cn(
        "flex shrink-0 items-center gap-1 rounded-full px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide",
        b.className,
      )}
      title={b.title}
    >
      {b.pulse && <span aria-hidden className="size-1.5 rounded-full bg-info motion-safe:animate-pulse" />}
      {b.label}
    </span>
  );
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
  runningTaskId,
  onStopRun,
  stopping = false,
}: {
  issue: Issue;
  isLive?: boolean;
  verdictInfo?: QAVerdictInfo;
  // The live QA task id for this issue (from the agent task snapshot), or null.
  // Only set on rows whose gate is running right now — enables the Stop button.
  runningTaskId?: string | null;
  // Cancel the running gate. The row lives inside an <AppLink>, so the button
  // stops propagation + prevents default to avoid navigating on click.
  onStopRun?: (taskId: string) => void;
  // True while this row's cancel request is in flight — disables the button.
  stopping?: boolean;
}) {
  const { t } = useT("issues");
  // One color-coded state chip (running / stale / fail / pass / pending) instead
  // of the old single "stale" + "live" pair — so the row's QA state is legible at
  // a glance in both the list lanes and the board cards.
  const state = qaRowState(issue, isLive);
  return (
    <div className="min-w-0 flex-1">
      <div className="flex items-center gap-2">
        <PriorityIcon priority={issue.priority} className="shrink-0" />
        <span className="w-14 shrink-0 text-xs text-muted-foreground">{issue.identifier}</span>
        <span className="min-w-0 flex-1 truncate">{issue.title}</span>
        <QAStateBadge state={state} />
        {state === "running" && runningTaskId && onStopRun && (
          <button
            type="button"
            disabled={stopping}
            onClick={(e) => {
              e.preventDefault();
              e.stopPropagation();
              onStopRun(runningTaskId);
            }}
            className="flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive disabled:opacity-50"
            title={t(($) => $.qa_cockpit.stop_run_title)}
            aria-label={t(($) => $.qa_cockpit.stop_run_title)}
          >
            {stopping ? <Loader2 className="size-3.5 animate-spin" /> : <CircleStop className="size-3.5" />}
          </button>
        )}
        {issue.assignee_type && issue.assignee_id && (
          <ActorAvatar actorType={issue.assignee_type} actorId={issue.assignee_id} size={20} enableHoverCard />
        )}
      </div>
      {verdictInfo?.summary && (
        <div className="mt-0.5 flex items-center gap-1.5 pl-6 text-[11px] text-muted-foreground">
          <span
            className="flex shrink-0 items-center gap-0.5 rounded border px-1 py-px text-[9px] uppercase tracking-wide"
            title={t(($) => $.qa_cockpit.verdict_source_title, { source: verdictInfo.source || "agent" })}
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
  runningTaskByIssue,
  onStopRun,
  stoppingTaskId,
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
  // Per-issue live QA task id (issueId → taskId) + the cancel handler, so a
  // running row can offer a Stop button. Off unless the caller wires them.
  runningTaskByIssue?: Map<string, string>;
  onStopRun?: (taskId: string) => void;
  stoppingTaskId?: string | null;
}) {
  const { t } = useT("issues");
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
        <p className="px-3 py-2 text-[12px] text-muted-foreground">{t(($) => $.qa_cockpit.nothing_here)}</p>
      ) : (
        // Bounded so one fat lane (e.g. a long-running "Pending") doesn't grow
        // the whole page — it scrolls internally instead.
        <ul className="max-h-[60vh] divide-y overflow-y-auto">
          {issues.map((issue) => (
            <li key={issue.id} className="flex items-center">
              {onToggleSelect && (
                <Checkbox
                  className="ml-3 shrink-0"
                  checked={selected?.has(issue.id) ?? false}
                  onCheckedChange={() => onToggleSelect(issue.id)}
                  aria-label={t(($) => $.qa_cockpit.select_issue_label, { identifier: issue.identifier })}
                />
              )}
              <AppLink
                href={href(issue.id)}
                className="flex min-w-0 flex-1 items-center gap-2 px-3 py-1.5 text-sm hover:bg-accent/60"
              >
                <QAIssueRow
                  issue={issue}
                  isLive={liveIssueIds?.has(issue.id)}
                  verdictInfo={verdicts?.[issue.id]}
                  runningTaskId={runningTaskByIssue?.get(issue.id) ?? null}
                  onStopRun={onStopRun}
                  stopping={
                    !!stoppingTaskId && stoppingTaskId === (runningTaskByIssue?.get(issue.id) ?? null)
                  }
                />
              </AppLink>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
