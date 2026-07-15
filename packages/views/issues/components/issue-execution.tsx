"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  Ban,
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Circle,
  Clock3,
  GitBranch,
  GitMerge,
  History,
  ListTree,
  Loader2,
  OctagonAlert,
  Play,
  RotateCcw,
  ShieldCheck,
  Sparkles,
} from "lucide-react";
import { api } from "@agora/core/api";
import { issueDetailOptions, issueKeys, issueOrchestrationOptions } from "@agora/core/issues/queries";
import { useWorkspaceId } from "@agora/core/hooks";
import { agentListOptions, squadListOptions } from "@agora/core/workspace/queries";
import {
  useApproveOrchestrationStep,
  useCancelOrchestrationBranch,
  useCreateIssueOrchestration,
  useEditIssueOrchestration,
  useRetryOrchestrationStep,
  useStartIssueOrchestration,
} from "@agora/core/issues/mutations";
import type {
  Agent,
  AgentTask,
  ExecutionStrategy,
  OrchestrationEvent,
  OrchestrationRun,
  OrchestrationStep,
  ProgressionPolicy,
} from "@agora/core/types";
import { useActorName } from "@agora/core/workspace/hooks";
import { Badge } from "@agora/ui/components/ui/badge";
import { Button } from "@agora/ui/components/ui/button";
import { Checkbox } from "@agora/ui/components/ui/checkbox";
import { Input } from "@agora/ui/components/ui/input";
import {
  NativeSelect,
  NativeSelectOption,
} from "@agora/ui/components/ui/native-select";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@agora/ui/components/ui/sheet";
import { cn } from "@agora/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { formatDuration } from "../../agents/components/agent-activity-hover-content";
import { useT } from "../../i18n";
import { useTaskLive } from "./stage-live-process";

const ACTIVE_TASK_STATUSES = new Set<AgentTask["status"]>([
  "queued",
  "dispatched",
  "waiting_local_directory",
  "running",
]);

const COMPLETE_STEP_STATUSES = new Set<OrchestrationStep["status"]>(["completed", "skipped"]);
const STALE_AFTER_MS = 5 * 60 * 1000;

// Brand-filled active segment. Layered over Button's `outline` variant, whose
// hover AND dark-mode rules would otherwise repaint the selected chip back to
// neutral. `outline` ships `dark:bg-input/30` / `dark:border-input`, which
// tailwind-merge treats as non-conflicting with the base `bg-brand` (different
// variant) — so the dark: rules win in dark theme unless explicitly overridden.
// bg/border/text are therefore re-pinned across base, hover, and dark states.
const SEGMENT_ACTIVE_CLASS =
  "border-brand bg-brand text-brand-foreground hover:bg-brand/90 hover:text-brand-foreground dark:border-brand dark:bg-brand dark:hover:bg-brand/90";

type IssuesT = ReturnType<typeof useT<"issues">>["t"];

function eventLabel(event: OrchestrationEvent, t: IssuesT): string {
  switch (event.kind) {
    case "plan_proposed": return t(($) => $.execution_surface.event_plan_proposed);
    case "plan_created": return t(($) => $.execution_surface.event_plan_created);
    case "plan_revised": return t(($) => $.execution_surface.event_plan_revised);
    case "step_queued": return t(($) => $.execution_surface.event_step_queued);
    case "step_completed": return t(($) => $.execution_surface.event_step_completed);
    case "step_failed": return t(($) => $.execution_surface.event_step_failed);
    case "step_retrying": return t(($) => $.execution_surface.event_step_retrying);
    case "step_retry_ready": return t(($) => $.execution_surface.event_step_retry_ready);
    case "step_retry_requested": return t(($) => $.execution_surface.event_step_retry_requested);
    case "step_cancelled": return t(($) => $.execution_surface.event_step_cancelled);
    case "branch_cancelled": return t(($) => $.execution_surface.event_branch_cancelled);
    case "approval_requested": return t(($) => $.execution_surface.event_approval_requested);
    case "step_approved": return t(($) => $.execution_surface.event_step_approved);
    case "dispatch_deferred": return t(($) => $.execution_surface.event_dispatch_deferred);
    case "dispatch_failed": return t(($) => $.execution_surface.event_dispatch_failed);
    case "progression_paused": return t(($) => $.execution_surface.event_progression_paused);
    case "run_completed": return t(($) => $.execution_surface.event_run_completed);
    case "run_failed": return t(($) => $.execution_surface.event_run_failed);
    case "run_cancelled": return t(($) => $.execution_surface.event_run_cancelled);
    default: return t(($) => $.execution_surface.event_updated);
  }
}

function statusTone(status: OrchestrationStep["status"]) {
  if (status === "completed") return "bg-success";
  if (status === "failed" || status === "cancelled") return "bg-destructive";
  if (status === "running" || status === "queued") return "bg-brand";
  if (status === "waiting_approval") return "bg-warning";
  if (status === "skipped") return "bg-muted-foreground/30";
  return "bg-border";
}

function runStatus(run: OrchestrationRun, t: IssuesT) {
  if (run.status === "draft") return { label: t(($) => $.execution_surface.status_plan_ready), tone: "text-foreground", dot: "bg-muted-foreground" };
  if (run.status === "waiting_approval") return { label: t(($) => $.execution_surface.status_action_required), tone: "text-warning", dot: "bg-warning" };
  if (run.status === "failed") return { label: t(($) => $.execution_surface.status_blocked), tone: "text-destructive", dot: "bg-destructive" };
  if (run.status === "completed") return { label: t(($) => $.execution_surface.status_complete), tone: "text-success", dot: "bg-success" };
  if (run.status === "cancelled") return { label: t(($) => $.execution_surface.status_cancelled), tone: "text-muted-foreground", dot: "bg-muted-foreground" };
  return { label: t(($) => $.execution_surface.status_working), tone: "text-brand", dot: "bg-brand" };
}

function stepStatusLabel(status: OrchestrationStep["status"], t: IssuesT) {
  switch (status) {
    case "queued": return t(($) => $.execution_surface.step_queued);
    case "running": return t(($) => $.execution_surface.step_running);
    case "waiting_approval": return t(($) => $.execution_surface.step_waiting_approval);
    case "completed": return t(($) => $.execution_surface.step_completed);
    case "failed": return t(($) => $.execution_surface.step_failed);
    case "cancelled": return t(($) => $.execution_surface.step_cancelled);
    case "skipped": return t(($) => $.execution_surface.step_skipped);
    default: return t(($) => $.execution_surface.step_pending);
  }
}

function strategyLabel(strategy: "automatic" | ExecutionStrategy, t: IssuesT) {
  switch (strategy) {
    case "solo": return t(($) => $.execution_surface.strategy_solo);
    case "squad": return t(($) => $.execution_surface.strategy_squad);
    case "human": return t(($) => $.execution_surface.strategy_human);
    case "custom": return t(($) => $.execution_surface.strategy_custom);
    default: return t(($) => $.execution_surface.strategy_automatic);
  }
}

function progressionLabel(progression: ProgressionPolicy, t: IssuesT) {
  switch (progression) {
    case "gated": return t(($) => $.execution_surface.progression_gated);
    case "manual": return t(($) => $.execution_surface.progression_manual);
    default: return t(($) => $.execution_surface.progression_automatic);
  }
}

function taskForStep(step: OrchestrationStep, taskByID: Map<string, AgentTask>) {
  return step.task_id ? taskByID.get(step.task_id) : undefined;
}

function actuallyWorking(step: OrchestrationStep, task?: AgentTask) {
  if (task) return task.status === "running";
  return step.status === "running";
}

function actuallyWaiting(step: OrchestrationStep, task?: AgentTask) {
  if (task) return ACTIVE_TASK_STATUSES.has(task.status) && task.status !== "running";
  return step.status === "queued";
}

function waitingReason(step: OrchestrationStep, stepByID: Map<string, OrchestrationStep>, t: IssuesT, task?: AgentTask, draft = false) {
  if (draft) return t(($) => $.execution_surface.waiting_proposed);
  if (task?.status === "waiting_local_directory") return t(($) => $.execution_surface.waiting_workspace);
  if (actuallyWaiting(step, task)) return t(($) => $.execution_surface.waiting_agent);
  const blockers = step.depends_on_step_ids
    .map((id) => stepByID.get(id))
    .filter((dependency): dependency is OrchestrationStep => !!dependency && !COMPLETE_STEP_STATUSES.has(dependency.status));
  if (blockers.length > 0) return t(($) => $.execution_surface.waiting_dependencies, { steps: blockers.map((dependency) => dependency.title).join(" + ") });
  return t(($) => $.execution_surface.waiting_capacity);
}

function StatusRail({ steps }: { steps: OrchestrationStep[] }) {
  return (
    <div className="grid h-1.5 min-w-24 flex-1 grid-flow-col auto-cols-fr gap-1" aria-hidden>
      {steps.map((step) => (
        <span key={step.id} className={cn("rounded-full transition-colors", statusTone(step.status))} />
      ))}
    </div>
  );
}

function ExecutionStatusBar({
  run,
  onOpen,
}: {
  run: OrchestrationRun;
  onOpen: () => void;
}) {
  const { t } = useT("issues");
  const startRun = useStartIssueOrchestration();
  const completed = run.steps.filter((step) => COMPLETE_STEP_STATUSES.has(step.status)).length;
  const active = run.steps.filter((step) => step.status === "running" || step.status === "queued").length;
  const status = runStatus(run, t);

  return (
    <div className="rounded-xl border bg-card px-3.5 py-3 shadow-xs sm:px-4">
      <p className="sr-only" aria-live="polite">
        {status.label}. {t(($) => $.detail.orchestration_progress, { completed, count: run.steps.length })}.
      </p>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span className={cn("flex items-center gap-2 text-xs font-semibold", status.tone)}>
          <span className={cn("size-2 rounded-full", status.dot, run.status === "running" && "motion-safe:animate-pulse")} aria-hidden />
          {status.label}
        </span>
        <span className="text-[11px] tabular-nums text-muted-foreground">
          {t(($) => $.detail.orchestration_progress, { completed, count: run.steps.length })}
          {active > 0 ? ` · ${t(($) => $.detail.orchestration_active, { count: active })}` : ""}
        </span>
        <StatusRail steps={run.steps} />
        {run.status === "draft" && (
          <Button size="sm" disabled={startRun.isPending} onClick={() => startRun.mutate(run.issue_id)}>
            {startRun.isPending ? <Loader2 className="animate-spin motion-reduce:animate-none" aria-hidden /> : <Play aria-hidden />}
            {t(($) => $.detail.orchestration_run)}
          </Button>
        )}
        <Button size="sm" variant="ghost" className="ml-auto" onClick={onOpen}>
          {t(($) => $.execution_surface.details)}
          <ChevronRight aria-hidden />
        </Button>
      </div>
    </div>
  );
}

function ActionCard({ issueId, step }: { issueId: string; step: OrchestrationStep }) {
  const { t } = useT("issues");
  const approve = useApproveOrchestrationStep();
  const retry = useRetryOrchestrationStep();
  const isRelease = step.stage === "release";
  const failed = step.status === "failed";
  const retryable = failed && step.attempt < step.max_attempts;

  return (
    <div className={cn("flex flex-col gap-3 rounded-lg border p-3 sm:flex-row sm:items-center", failed ? "border-destructive/30 bg-destructive/[0.035]" : "border-warning/35 bg-warning/[0.045]")}>
      <div className="flex min-w-0 flex-1 gap-2.5">
        <span className={cn("mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md", failed ? "bg-destructive/10 text-destructive" : "bg-warning/10 text-warning")}>
          {failed ? <OctagonAlert className="size-4" aria-hidden /> : <ShieldCheck className="size-4" aria-hidden />}
        </span>
        <div className="min-w-0">
          <p className="text-xs font-semibold">
            {failed
              ? t(($) => $.execution_surface.action_step_attention, { title: step.title })
              : isRelease
                ? t(($) => $.execution_surface.action_release_ready)
                : t(($) => $.execution_surface.action_step_ready, { title: step.title })}
          </p>
          <p className="mt-0.5 text-[11px] text-muted-foreground">
            {failed
              ? step.integration_status === "conflicts"
                ? t(($) => $.execution_surface.action_conflicts)
                : step.error || t(($) => $.execution_surface.action_stopped)
              : step.depends_on_step_ids.length > 0
                ? t(($) => $.execution_surface.action_prerequisites, { count: step.depends_on_step_ids.length })
                : t(($) => $.execution_surface.action_approve_hint)}
          </p>
        </div>
      </div>
      {step.status === "waiting_approval" && (
        <Button size="sm" disabled={approve.isPending} onClick={() => approve.mutate({ issueId, stepId: step.id })}>
          {approve.isPending ? <Loader2 className="animate-spin motion-reduce:animate-none" aria-hidden /> : <ShieldCheck aria-hidden />}
          {isRelease ? t(($) => $.execution_surface.approve_release) : t(($) => $.execution_surface.approve)}
        </Button>
      )}
      {retryable && (
        <Button size="sm" variant="outline" disabled={retry.isPending} onClick={() => retry.mutate({ issueId, stepId: step.id })}>
          {retry.isPending ? <Loader2 className="animate-spin motion-reduce:animate-none" aria-hidden /> : <RotateCcw aria-hidden />}
          {t(($) => $.detail.orchestration_retry)}
        </Button>
      )}
    </div>
  );
}

function WorkingCard({ issueId, step, task }: { issueId: string; step: OrchestrationStep; task?: AgentTask }) {
  const { t } = useT("issues");
  const { getActorName } = useActorName();
  const cancel = useCancelOrchestrationBranch();
  const { todos, headline, latestMessageAt } = useTaskLive(step.task_id ?? "");
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 15_000);
    return () => window.clearInterval(timer);
  }, []);

  const startedAt = task?.started_at ?? task?.dispatched_at ?? task?.created_at;
  const elapsed = startedAt ? formatDuration(startedAt, now) : "";
  const lastUpdate = latestMessageAt ?? startedAt;
  const stale = !!lastUpdate && now - new Date(lastUpdate).getTime() >= STALE_AFTER_MS;
  const staleFor = stale && lastUpdate ? formatDuration(lastUpdate, now) : "";
  const done = todos.filter((todo) => todo.status === "completed").length;
  const current = todos.find((todo) => todo.status === "in_progress");
  const agentName = step.agent_id ? getActorName("agent", step.agent_id) : t(($) => $.execution_surface.assigned_agent);

  return (
    <article className="relative overflow-hidden rounded-xl border bg-card p-4 shadow-xs">
      <span className="absolute inset-y-0 left-0 w-0.5 bg-brand" aria-hidden />
      <div className="flex items-start gap-3">
        {step.agent_id ? (
          <ActorAvatar actorType="agent" actorId={step.agent_id} size={30} showStatusDot />
        ) : (
          <span className="flex size-[30px] items-center justify-center rounded-full bg-muted"><Sparkles className="size-3.5" /></span>
        )}
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="truncate text-xs font-semibold">{agentName}</span>
            <span className="text-[10px] capitalize text-muted-foreground">{step.capability.replaceAll("_", " ")}</span>
            {elapsed && <span className="ml-auto font-mono text-[10px] tabular-nums text-muted-foreground">{elapsed}</span>}
          </div>
          <h3 className="mt-2 text-sm font-medium leading-snug">{step.title}</h3>
          <p className={cn("mt-1 text-xs leading-relaxed", stale ? "text-warning" : "text-muted-foreground")}>
            {stale
              ? t(($) => $.execution_surface.no_update_for, { elapsed: staleFor })
              : headline || t(($) => $.execution_surface.starting_step, { title: step.title })}
          </p>
          {todos.length > 0 && (
            <div className="mt-3 rounded-md bg-muted/45 px-2.5 py-2">
              <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
                <span className="font-mono tabular-nums">{done}/{todos.length}</span>
                <span className="min-w-0 truncate">{current ? current.activeForm || current.content : t(($) => $.execution_surface.work_checklist)}</span>
              </div>
            </div>
          )}
        </div>
        <Button
          size="icon-sm"
          variant="ghost"
          disabled={cancel.isPending}
          onClick={() => cancel.mutate({ issueId, stepId: step.id })}
          aria-label={t(($) => $.execution_surface.cancel_step, { title: step.title })}
          title={t(($) => $.execution_surface.cancel_step, { title: step.title })}
        >
          <Ban aria-hidden />
        </Button>
      </div>
    </article>
  );
}

function WaitingRow({ step, reason }: { step: OrchestrationStep; reason: string }) {
  return (
    <li className="flex items-center gap-2.5 py-2 text-xs">
      {step.kind === "integration" ? <GitMerge className="size-3.5 shrink-0 text-muted-foreground" aria-hidden /> : <Circle className="size-3 shrink-0 text-muted-foreground/50" aria-hidden />}
      <span className="min-w-0 flex-1 truncate font-medium text-foreground/85">{step.title}</span>
      <span className="hidden min-w-0 max-w-[55%] truncate text-[11px] text-muted-foreground sm:block">{reason}</span>
      <Badge variant="secondary" className="h-5 shrink-0 text-[9px] capitalize">{step.capability}</Badge>
    </li>
  );
}

function ActiveWork({ run, tasks }: { run: OrchestrationRun; tasks: AgentTask[] }) {
  const { t } = useT("issues");
  const taskByID = useMemo(() => new Map(tasks.map((task) => [task.id, task])), [tasks]);
  const stepByID = useMemo(() => new Map(run.steps.map((step) => [step.id, step])), [run.steps]);
  const action = run.steps.filter((step) => step.status === "waiting_approval" || step.status === "failed");
  const working = run.steps.filter((step) => actuallyWorking(step, taskForStep(step, taskByID)));
  const waiting = run.steps.filter((step) => {
    if (action.includes(step) || working.includes(step) || COMPLETE_STEP_STATUSES.has(step.status)) return false;
    return step.status !== "cancelled" && step.status !== "skipped";
  });
  const completed = run.steps.filter((step) => COMPLETE_STEP_STATUSES.has(step.status));
  const latestHandoff = [...completed].sort((a, b) => b.position - a.position)[0];

  return (
    <section className="mt-3 space-y-5 rounded-xl border bg-background px-4 py-4 sm:px-5" aria-labelledby={`active-work-${run.id}`}>
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 id={`active-work-${run.id}`} className="text-sm font-semibold">{t(($) => $.execution_surface.active_work)}</h2>
          <p className="mt-0.5 text-[11px] text-muted-foreground">{t(($) => $.execution_surface.active_work_hint)}</p>
        </div>
        <Badge variant="outline" className="h-5 text-[9px]">{strategyLabel(run.execution_strategy, t)}</Badge>
      </div>

      {action.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-[10px] font-semibold uppercase tracking-[0.14em] text-warning">{t(($) => $.execution_surface.action_required)}</h3>
          {action.map((step) => <ActionCard key={step.id} issueId={run.issue_id} step={step} />)}
        </div>
      )}

      {working.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-[10px] font-semibold uppercase tracking-[0.14em] text-muted-foreground">{t(($) => $.execution_surface.working_now)}</h3>
          <div className={cn("grid gap-3", working.length > 1 && "md:grid-cols-2")}>
            {working.map((step) => <WorkingCard key={step.id} issueId={run.issue_id} step={step} task={taskForStep(step, taskByID)} />)}
          </div>
        </div>
      )}

      {waiting.length > 0 && (
        <details className="group" open={working.length === 0 && action.length === 0}>
          <summary className="flex cursor-pointer list-none items-center gap-2 text-[10px] font-semibold uppercase tracking-[0.14em] text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
            <ChevronDown className="size-3.5 transition-transform group-open:rotate-180 motion-reduce:transition-none" aria-hidden />
            {t(($) => $.execution_surface.waiting)}
            <span className="font-mono tabular-nums">{waiting.length}</span>
          </summary>
          <ul className="mt-2 divide-y divide-border/60 rounded-lg border px-3">
            {waiting.map((step) => <WaitingRow key={step.id} step={step} reason={waitingReason(step, stepByID, t, taskForStep(step, taskByID), run.status === "draft")} />)}
          </ul>
        </details>
      )}

      {completed.length > 0 && (
        <div className="flex items-center gap-2 border-t pt-3 text-[11px] text-muted-foreground">
          <CheckCircle2 className="size-3.5 shrink-0 text-success" aria-hidden />
          <span className="font-medium text-foreground/80">{t(($) => $.execution_surface.completed, { count: completed.length })}</span>
          {latestHandoff && <span className="min-w-0 truncate">{t(($) => $.execution_surface.latest_handoff, { title: latestHandoff.title })}</span>}
        </div>
      )}
    </section>
  );
}

function DraftRouteControl({ run, step, agents }: { run: OrchestrationRun; step: OrchestrationStep; agents: Agent[] }) {
  const { t } = useT("issues");
  const editPlan = useEditIssueOrchestration();
  if (
    run.status !== "draft" ||
    step.status !== "pending" ||
    !step.agent_id ||
    step.stage === "plan" ||
    step.stage === "release" ||
    step.kind === "integration"
  ) {
    return null;
  }
  const candidates = agents.filter((agent) => !agent.archived_at);
  return (
    <label className="mt-2 flex items-center justify-between gap-3 border-t pt-2 text-[10px] text-muted-foreground">
      <span>{t(($) => $.execution_surface.route_step)}</span>
      <NativeSelect
        size="sm"
        className="max-w-56"
        value={step.agent_id}
        disabled={editPlan.isPending}
        aria-label={t(($) => $.execution_surface.route_step_label, { title: step.title })}
        onChange={(event) => {
          const agentId = event.target.value;
          if (!agentId || agentId === step.agent_id) return;
          editPlan.mutate(
            {
              issueId: run.issue_id,
              data: {
                expected_version: run.plan_version,
                reason: t(($) => $.execution_surface.route_revision_reason, { title: step.title }),
                operation: "reroute",
                step_id: step.id,
                agent_id: agentId,
                instructions: step.instructions,
              },
            },
            {
              onSuccess: () => toast.success(t(($) => $.execution_surface.route_changed)),
              onError: (error) => toast.error(
                error instanceof Error && error.message
                  ? error.message
                  : t(($) => $.execution_surface.route_failed),
              ),
            },
          );
        }}
      >
        {candidates.map((agent) => (
          <NativeSelectOption key={agent.id} value={agent.id}>{agent.name}</NativeSelectOption>
        ))}
      </NativeSelect>
    </label>
  );
}

function AdvancedStep({ run, step, stepByID, agents }: { run: OrchestrationRun; step: OrchestrationStep; stepByID: Map<string, OrchestrationStep>; agents: Agent[] }) {
  const { t } = useT("issues");
  const dependencies = step.depends_on_step_ids.map((id) => stepByID.get(id)?.title).filter(Boolean).join(" + ");
  return (
    <li className="relative grid grid-cols-[1.5rem_1fr] gap-2 pb-4 last:pb-0">
      <span className={cn("relative z-10 flex size-6 items-center justify-center rounded-full border bg-popover", step.status === "completed" && "border-success/40 text-success", step.status === "failed" && "border-destructive/40 text-destructive", (step.status === "running" || step.status === "queued") && "border-brand/40 text-brand")}>
        {step.status === "completed" ? <Check className="size-3.5" /> : step.kind === "integration" ? <GitMerge className="size-3.5" /> : step.status === "failed" ? <OctagonAlert className="size-3.5" /> : <Circle className="size-2.5" />}
      </span>
      <span className="absolute bottom-0 left-3 top-6 w-px bg-border last:hidden" aria-hidden />
      <div className="min-w-0 rounded-lg border bg-background p-3">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-xs font-medium">{step.title}</span>
          <Badge variant="secondary" className="h-4 px-1 text-[9px] capitalize">{step.capability}</Badge>
          <span className="ml-auto text-[9px] uppercase tracking-wide text-muted-foreground">{stepStatusLabel(step.status, t)}</span>
        </div>
        {dependencies && <p className="mt-1 text-[10px] text-muted-foreground">{t(($) => $.detail.orchestration_after, { steps: dependencies })}</p>}
        <dl className="mt-2 grid gap-x-3 gap-y-1 text-[10px] sm:grid-cols-2">
          {step.model && <div className="min-w-0"><dt className="inline text-muted-foreground">{t(($) => $.execution_surface.model)}</dt><dd className="inline break-all font-mono">{step.model}</dd></div>}
          {step.worktree_branch && <div className="min-w-0"><dt className="inline text-muted-foreground">{t(($) => $.execution_surface.branch)}</dt><dd className="inline break-all font-mono">{step.worktree_branch}</dd></div>}
          {step.base_sha && <div className="min-w-0"><dt className="inline text-muted-foreground">{t(($) => $.execution_surface.base)}</dt><dd className="inline font-mono">{step.base_sha.slice(0, 12)}</dd></div>}
          {step.head_sha && <div className="min-w-0"><dt className="inline text-muted-foreground">{t(($) => $.execution_surface.head)}</dt><dd className="inline font-mono">{step.head_sha.slice(0, 12)}</dd></div>}
        </dl>
        <DraftRouteControl run={run} step={step} agents={agents} />
      </div>
    </li>
  );
}

function ExecutionDrawer({ run, open, onOpenChange, agents }: { run: OrchestrationRun; open: boolean; onOpenChange: (open: boolean) => void; agents: Agent[] }) {
  const { t } = useT("issues");
  const stepByID = new Map(run.steps.map((step) => [step.id, step]));
  const history = [
    ...run.events.map((value) => ({ kind: "event" as const, id: value.id, createdAt: value.created_at, value })),
    ...run.revisions.map((value) => ({ kind: "revision" as const, id: value.id, createdAt: value.created_at, value })),
  ].sort((a, b) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime());
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-[min(94vw,42rem)] gap-0 sm:max-w-2xl">
        <SheetHeader className="border-b pr-12">
          <SheetTitle className="flex items-center gap-2"><ListTree className="size-4 text-brand" aria-hidden />{t(($) => $.execution_surface.drawer_title)}</SheetTitle>
          <SheetDescription>{t(($) => $.execution_surface.drawer_description, { version: run.plan_version, strategy: strategyLabel(run.execution_strategy, t), progression: progressionLabel(run.progression_policy, t) })}</SheetDescription>
        </SheetHeader>
        <div className="flex-1 overflow-y-auto px-4 py-5 sm:px-5">
          <section aria-labelledby={`full-plan-${run.id}`}>
            <div className="mb-3 flex items-center justify-between">
              <h2 id={`full-plan-${run.id}`} className="text-xs font-semibold">{t(($) => $.execution_surface.full_plan)}</h2>
              <Badge variant="outline" className="h-5 text-[9px]">{runStatus(run, t).label}</Badge>
            </div>
            {run.status === "draft" && (
              <p className="mb-3 text-[11px] leading-relaxed text-muted-foreground">
                {t(($) => $.execution_surface.proposal_edit_hint)}
              </p>
            )}
            <ol>{run.steps.map((step) => <AdvancedStep key={step.id} run={run} step={step} stepByID={stepByID} agents={agents} />)}</ol>
          </section>

          {(run.events.length > 0 || run.revisions.length > 0) && (
            <section className="mt-7 border-t pt-5" aria-labelledby={`history-${run.id}`}>
              <h2 id={`history-${run.id}`} className="flex items-center gap-2 text-xs font-semibold"><History className="size-3.5" aria-hidden />{t(($) => $.execution_surface.handoffs)}</h2>
              <div className="mt-3 space-y-2">
                {history.map((item) => item.kind === "event" ? (
                  <div key={`event-${item.id}`} className="flex items-start gap-2 text-[11px] text-muted-foreground">
                    <Clock3 className="mt-0.5 size-3 shrink-0" aria-hidden />
                    <span className="min-w-0 flex-1">{eventLabel(item.value, t)}</span>
                    <time className="shrink-0 font-mono text-[9px] tabular-nums" dateTime={item.createdAt}>{new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(new Date(item.createdAt))}</time>
                  </div>
                ) : (
                  <div key={`revision-${item.id}`} className="flex items-start gap-2 text-[11px] text-muted-foreground">
                    <GitBranch className="mt-0.5 size-3 shrink-0" aria-hidden />
                    <span className="font-medium text-foreground/80">{t(($) => $.detail.orchestration_version, { version: item.value.version })}</span>
                    <span className="min-w-0 flex-1">{item.value.reason}</span>
                    <time className="shrink-0 font-mono text-[9px] tabular-nums" dateTime={item.createdAt}>{new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(new Date(item.createdAt))}</time>
                  </div>
                ))}
              </div>
            </section>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

export function IssueExecution({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [customizing, setCustomizing] = useState(false);
  const [strategy, setStrategy] = useState<"automatic" | ExecutionStrategy>("automatic");
  const [progression, setProgression] = useState<ProgressionPolicy>("automatic");
  const [squadId, setSquadId] = useState("");
  const [maxConcurrency, setMaxConcurrency] = useState(3);
  const [reviewPlanFirst, setReviewPlanFirst] = useState(false);
  const { data: run, isLoading } = useQuery(issueOrchestrationOptions(issueId));
  const wsId = useWorkspaceId();
  const { data: issue } = useQuery(issueDetailOptions(wsId, issueId));
  const { data: squads = [] } = useQuery(squadListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const activeSquads = useMemo(() => squads.filter((squad) => !squad.archived_at), [squads]);
  const assignedSquadId = issue?.assignee_type === "squad" ? issue.assignee_id ?? "" : "";
  useEffect(() => {
    if (!squadId && assignedSquadId && activeSquads.some((squad) => squad.id === assignedSquadId)) {
      setSquadId(assignedSquadId);
    }
  }, [activeSquads, assignedSquadId, squadId]);
  const { data: tasks = [] } = useQuery({
    queryKey: issueKeys.tasks(issueId),
    queryFn: () => api.listTasksByIssue(issueId),
    enabled: !!run,
    staleTime: 30_000,
  });
  const createRun = useCreateIssueOrchestration();

  const createWithOptions = () => {
    createRun.mutate(
      {
        issueId,
        data: {
          auto_start: !reviewPlanFirst,
          ...(strategy === "automatic" ? {} : { execution_strategy: strategy }),
          ...(strategy === "squad" && squadId ? { squad_id: squadId } : {}),
          progression_policy: progression,
          policy: { max_concurrency: maxConcurrency },
        },
      },
      {
        onError: (error) => toast.error(
          error instanceof Error && error.message
            ? error.message
            : t(($) => $.execution_surface.create_failed),
        ),
      },
    );
  };

  const squadSelectionMissing = strategy === "squad" && !squadId;

  if (isLoading) return <div className="h-24 animate-pulse rounded-xl border bg-muted/30 motion-reduce:animate-none" aria-hidden />;

  if (!run) {
    return (
      <section className="overflow-hidden rounded-xl border border-dashed">
        <div className="flex flex-col items-start justify-between gap-3 px-4 py-4 sm:flex-row sm:items-center">
          <div className="min-w-0">
            <h2 className="text-sm font-semibold">{t(($) => $.detail.orchestration_title)}</h2>
            <p className="mt-0.5 max-w-xl text-xs text-muted-foreground">{t(($) => $.detail.orchestration_empty)}</p>
          </div>
          <div className="flex shrink-0 items-center gap-1.5">
            <Button size="sm" variant="ghost" onClick={() => setCustomizing((open) => !open)} aria-expanded={customizing}>
              {t(($) => $.execution_surface.customize)}
              <ChevronDown className={cn("transition-transform motion-reduce:transition-none", customizing && "rotate-180")} aria-hidden />
            </Button>
            <Button size="sm" disabled={createRun.isPending} onClick={() => createRun.mutate({ issueId, data: { auto_start: true } })}>
              {createRun.isPending ? <Loader2 className="animate-spin motion-reduce:animate-none" aria-hidden /> : <Play aria-hidden />}
              {t(($) => $.detail.orchestration_start)}
            </Button>
          </div>
        </div>
        {customizing && (
          <div className="grid gap-5 border-t bg-muted/15 px-4 py-4 sm:grid-cols-2">
            <fieldset>
              <legend className="text-[10px] font-semibold uppercase tracking-[0.14em] text-muted-foreground">{t(($) => $.execution_surface.strategy_legend)}</legend>
              <div className="mt-2 flex flex-wrap gap-1.5">
                {(["automatic", "solo", "squad", "human"] as const).map((option) => (
                  <Button
                    key={option}
                    type="button"
                    size="sm"
                    variant="outline"
                    className={cn("h-7 capitalize", strategy === option && SEGMENT_ACTIVE_CLASS)}
                    onClick={() => setStrategy(option)}
                    aria-pressed={strategy === option}
                  >
                    {strategyLabel(option, t)}
                  </Button>
                ))}
              </div>
              <p className="mt-2 text-[11px] text-muted-foreground">{t(($) => $.execution_surface.strategy_hint)}</p>
              {strategy === "squad" && (
                <label className="mt-3 block text-xs">
                  <span className="font-medium">{t(($) => $.execution_surface.squad_label)}</span>
                  <NativeSelect
                    name="orchestration_squad"
                    value={squadId}
                    onChange={(event) => setSquadId(event.target.value)}
                    size="sm"
                    className="mt-1.5 w-full text-xs"
                  >
                    <NativeSelectOption value="">
                      {activeSquads.length > 0
                        ? t(($) => $.execution_surface.squad_placeholder)
                        : t(($) => $.execution_surface.squad_empty)}
                    </NativeSelectOption>
                    {activeSquads.map((squad) => (
                      <NativeSelectOption key={squad.id} value={squad.id}>{squad.name}</NativeSelectOption>
                    ))}
                  </NativeSelect>
                  <span className={cn("mt-1 block text-[11px]", squadSelectionMissing ? "text-warning" : "text-muted-foreground")}>
                    {activeSquads.length > 0
                      ? t(($) => $.execution_surface.squad_hint)
                      : t(($) => $.execution_surface.squad_empty_hint)}
                  </span>
                </label>
              )}
            </fieldset>

            <fieldset>
              <legend className="text-[10px] font-semibold uppercase tracking-[0.14em] text-muted-foreground">{t(($) => $.execution_surface.progression_legend)}</legend>
              <div className="mt-2 flex flex-wrap gap-1.5">
                {(["automatic", "gated", "manual"] as const).map((option) => (
                  <Button
                    key={option}
                    type="button"
                    size="sm"
                    variant="outline"
                    className={cn("h-7 capitalize", progression === option && SEGMENT_ACTIVE_CLASS)}
                    onClick={() => setProgression(option)}
                    aria-pressed={progression === option}
                  >
                    {progressionLabel(option, t)}
                  </Button>
                ))}
              </div>
              <p className="mt-2 text-[11px] text-muted-foreground">{t(($) => $.execution_surface.progression_hint)}</p>
            </fieldset>

            <label className="flex items-center justify-between gap-4 text-xs">
              <span>
                <span className="font-medium">{t(($) => $.execution_surface.parallel_workers)}</span>
                <span className="mt-0.5 block text-[11px] text-muted-foreground">{t(($) => $.execution_surface.parallel_workers_hint)}</span>
              </span>
              <Input
                type="number"
                name="orchestration_max_concurrency"
                min={1}
                max={10}
                value={maxConcurrency}
                onChange={(event) => setMaxConcurrency(Math.min(10, Math.max(1, Number(event.target.value) || 1)))}
                className="w-16 text-right font-mono text-xs tabular-nums"
              />
            </label>

            <div className="flex flex-col justify-between gap-3 sm:items-end">
              <label className="flex cursor-pointer items-center gap-2 text-xs">
                <Checkbox
                  name="orchestration_review_plan_first"
                  checked={reviewPlanFirst}
                  onCheckedChange={(checked) => setReviewPlanFirst(checked === true)}
                />
                {t(($) => $.execution_surface.review_plan_first)}
              </label>
              <Button size="sm" disabled={createRun.isPending || squadSelectionMissing} onClick={createWithOptions}>
                {createRun.isPending ? <Loader2 className="animate-spin motion-reduce:animate-none" aria-hidden /> : reviewPlanFirst ? <ListTree aria-hidden /> : <Play aria-hidden />}
                {reviewPlanFirst ? t(($) => $.execution_surface.create_proposal) : t(($) => $.execution_surface.build_and_run)}
              </Button>
            </div>
          </div>
        )}
      </section>
    );
  }

  return (
    <div>
      <ExecutionStatusBar run={run} onOpen={() => setDrawerOpen(true)} />
      <ActiveWork run={run} tasks={tasks} />
      <ExecutionDrawer run={run} open={drawerOpen} onOpenChange={setDrawerOpen} agents={agents} />
    </div>
  );
}
