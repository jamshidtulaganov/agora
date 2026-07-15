"use client";

import { useQuery } from "@tanstack/react-query";
import {
  Ban,
  Check,
  ChevronDown,
  Circle,
  Clock3,
  GitBranch,
  GitMerge,
  Loader2,
  OctagonAlert,
  Play,
  RotateCcw,
  ShieldCheck,
} from "lucide-react";
import { issueOrchestrationOptions } from "@agora/core/issues/queries";
import {
  useApproveOrchestrationStep,
  useCancelOrchestrationBranch,
  useCreateIssueOrchestration,
  useRetryOrchestrationStep,
  useStartIssueOrchestration,
} from "@agora/core/issues/mutations";
import type { OrchestrationEvent, OrchestrationStep } from "@agora/core/types";
import { Badge } from "@agora/ui/components/ui/badge";
import { Button } from "@agora/ui/components/ui/button";
import { cn } from "@agora/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n";

type DisplayStep = { step: OrchestrationStep; depth: number };

/**
 * API positions preserve plan-revision history, so a child added later can
 * have a higher position than a release gate. Display children directly under
 * their parent to make squad ownership and convergence understandable.
 */
export function arrangeOrchestrationSteps(steps: OrchestrationStep[]): DisplayStep[] {
  const byID = new Map(steps.map((step) => [step.id, step]));
  const children = new Map<string, OrchestrationStep[]>();
  const compare = (a: OrchestrationStep, b: OrchestrationStep) => a.position - b.position || a.id.localeCompare(b.id);

  for (const step of steps) {
    if (!step.parent_step_id || !byID.has(step.parent_step_id)) continue;
    const siblings = children.get(step.parent_step_id) ?? [];
    siblings.push(step);
    children.set(step.parent_step_id, siblings);
  }
  for (const siblings of children.values()) siblings.sort(compare);

  const arranged: DisplayStep[] = [];
  const visited = new Set<string>();
  const visit = (step: OrchestrationStep, depth: number) => {
    if (visited.has(step.id)) return;
    visited.add(step.id);
    arranged.push({ step, depth });
    for (const child of children.get(step.id) ?? []) visit(child, depth + 1);
  };

  for (const step of [...steps].sort(compare)) {
    if (!step.parent_step_id || !byID.has(step.parent_step_id)) visit(step, 0);
  }
  // Defensive fallback for malformed/cyclic historical data.
  for (const step of [...steps].sort(compare)) visit(step, 0);
  return arranged;
}

function StepIcon({ step }: { step: OrchestrationStep }) {
  if (step.status === "completed") {
    return step.kind === "integration" ? <GitMerge className="size-3.5" aria-hidden /> : <Check className="size-3.5" aria-hidden />;
  }
  if (step.status === "failed") return <OctagonAlert className="size-3.5" aria-hidden />;
  if (step.status === "waiting_approval") return <ShieldCheck className="size-3.5" aria-hidden />;
  if (step.status === "queued" || step.status === "running") {
    return <Loader2 className="size-3.5 animate-spin motion-reduce:animate-none" aria-hidden />;
  }
  if (step.kind === "integration") return <GitMerge className="size-3.5" aria-hidden />;
  return <Circle className="size-3" aria-hidden />;
}

function progressTone(status: OrchestrationStep["status"]) {
  if (status === "completed") return "bg-success";
  if (status === "failed" || status === "cancelled") return "bg-destructive";
  if (status === "running" || status === "queued") return "bg-brand";
  if (status === "waiting_approval") return "bg-warning";
  if (status === "skipped") return "bg-muted-foreground/30";
  return "bg-border";
}

export function OrchestrationTimeline({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const { data: run, isLoading } = useQuery(issueOrchestrationOptions(issueId));
  const createRun = useCreateIssueOrchestration();
  const startRun = useStartIssueOrchestration();
  const approveStep = useApproveOrchestrationStep();
  const cancelBranch = useCancelOrchestrationBranch();
  const retryStep = useRetryOrchestrationStep();
  const eventLabel = (event: OrchestrationEvent): string => {
    switch (event.kind) {
      case "plan_created":
        return t(($) => $.detail.orchestration_event_plan_created);
      case "step_queued":
        return t(($) => $.detail.orchestration_event_step_queued);
      case "step_completed":
        return t(($) => $.detail.orchestration_event_step_completed);
      case "step_failed":
        return t(($) => $.detail.orchestration_event_step_failed);
      case "step_retrying":
        return t(($) => $.detail.orchestration_event_step_retrying);
      case "approval_requested":
        return t(($) => $.detail.orchestration_event_approval_requested);
      case "step_approved":
        return t(($) => $.detail.orchestration_event_step_approved);
      case "run_completed":
        return t(($) => $.detail.orchestration_event_run_completed);
      default:
        return event.kind.replaceAll("_", " ");
    }
  };

  if (isLoading) {
    return <div className="h-24 animate-pulse rounded-xl border bg-muted/30 motion-reduce:animate-none" aria-hidden />;
  }

  if (!run) {
    return (
      <section className="flex items-center justify-between gap-4 rounded-xl border border-dashed px-4 py-3.5">
        <div className="min-w-0">
          <h2 className="text-pretty text-sm font-semibold">{t(($) => $.detail.orchestration_title)}</h2>
          <p className="mt-0.5 text-pretty text-xs text-muted-foreground">{t(($) => $.detail.orchestration_empty)}</p>
        </div>
        <Button
          size="sm"
          className="shrink-0"
          disabled={createRun.isPending}
          onClick={() => createRun.mutate({ issueId, data: { auto_start: true } })}
        >
          {createRun.isPending ? <Loader2 className="animate-spin motion-reduce:animate-none" aria-hidden /> : <Play aria-hidden />}
          {t(($) => $.detail.orchestration_start)}
        </Button>
      </section>
    );
  }

  const events = run.events.slice(-8).reverse();
  const displaySteps = arrangeOrchestrationSteps(run.steps);
  const stepByID = new Map(run.steps.map((step) => [step.id, step]));
  const completedCount = run.steps.filter((step) => step.status === "completed" || step.status === "skipped").length;
  const activeCount = run.steps.filter((step) => step.status === "running" || step.status === "queued").length;

  return (
    <section className="overflow-hidden rounded-xl border bg-card" aria-labelledby={`orchestration-${run.id}`}>
      <p className="sr-only" aria-live="polite">
        {t(($) => $.detail.orchestration_progress, { completed: completedCount, count: run.steps.length })}. {run.status.replaceAll("_", " ")}.
      </p>

      <header className="border-b px-4 py-4 sm:px-5">
        <div className="flex flex-col justify-between gap-3 sm:flex-row sm:items-start">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h2 id={`orchestration-${run.id}`} className="text-pretty text-sm font-semibold">
                {t(($) => $.detail.orchestration_title)}
              </h2>
              <Badge variant="outline" className="h-5 text-[10px] capitalize">
                {run.status.replaceAll("_", " ")}
              </Badge>
              <Badge variant="secondary" className="h-5 text-[10px] tabular-nums">
                {t(($) => $.detail.orchestration_version, { version: run.plan_version })}
              </Badge>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              {t(($) => $.detail.orchestration_progress, { completed: completedCount, count: run.steps.length })}
              {activeCount > 0 && <> · {t(($) => $.detail.orchestration_active, { count: activeCount })}</>}
            </p>
          </div>

          <div className="flex flex-wrap items-center gap-3 text-[11px] text-muted-foreground">
            {run.owner_id && run.owner_type !== "unassigned" && (
              <span className="flex items-center gap-1.5">
                <span>{t(($) => $.detail.orchestration_owner)}</span>
                <ActorAvatar actorType={run.owner_type} actorId={run.owner_id} size={18} enableHoverCard />
              </span>
            )}
            {run.controller_agent_id && (
              <span className="flex items-center gap-1.5">
                <span>{t(($) => $.detail.orchestration_controller)}</span>
                <ActorAvatar actorType="agent" actorId={run.controller_agent_id} size={18} enableHoverCard />
              </span>
            )}
            <Badge variant="secondary" className="h-5 text-[10px] capitalize">
              {run.execution_strategy}
            </Badge>
            {run.status === "draft" && (
              <Button size="sm" disabled={startRun.isPending} onClick={() => startRun.mutate(issueId)}>
                {startRun.isPending ? <Loader2 className="animate-spin motion-reduce:animate-none" aria-hidden /> : <Play aria-hidden />}
                {t(($) => $.detail.orchestration_run)}
              </Button>
            )}
          </div>
        </div>

        <div className="mt-3 grid h-1.5 grid-flow-col auto-cols-fr gap-1" aria-hidden>
          {displaySteps.map(({ step }) => (
            <span key={step.id} className={cn("rounded-full", progressTone(step.status))} />
          ))}
        </div>
      </header>

      <ol className="px-3 py-2 sm:px-5" aria-label={t(($) => $.detail.orchestration_title)}>
        {displaySteps.map(({ step, depth }, index) => {
          const dependencies = step.depends_on_step_ids.map((id) => stepByID.get(id)).filter(Boolean) as OrchestrationStep[];
          const dependencyNames = dependencies.map((dependency) => dependency.title).join(" + ");
          const isActive = step.status === "queued" || step.status === "running";
          const isIntegration = step.kind === "integration";
          const isLast = index === displaySteps.length - 1;

          return (
            <li
              key={step.id}
              className={cn(
                "relative py-1.5",
                depth > 0 && "before:absolute before:-left-3 before:top-0 before:h-1/2 before:w-3 before:rounded-bl-lg before:border-b before:border-l before:border-border",
              )}
              style={{ marginLeft: `${Math.min(depth, 3) * 1.5}rem` }}
            >
              {!isLast && depth === 0 && <span className="absolute left-3 top-9 h-[calc(100%-1rem)] w-px bg-border" aria-hidden />}
              <div
                className={cn(
                  "relative flex flex-col gap-3 rounded-lg px-1 py-2.5 sm:flex-row sm:items-center",
                  isIntegration && "border border-brand/25 bg-brand/[0.035] px-3",
                  step.status === "failed" && "border border-destructive/25 bg-destructive/[0.035] px-3",
                )}
              >
                <div className="flex min-w-0 flex-1 gap-3">
                  <span
                    className={cn(
                      "relative z-10 flex size-6 shrink-0 items-center justify-center rounded-full border bg-background text-muted-foreground",
                      step.status === "completed" && "border-success/40 bg-success/10 text-success",
                      step.status === "failed" && "border-destructive/40 bg-destructive/10 text-destructive",
                      step.status === "waiting_approval" && "border-warning/40 bg-warning/10 text-warning",
                      isActive && "border-brand/40 bg-brand/10 text-brand",
                      isIntegration && "rounded-md border-brand/40 text-brand",
                    )}
                  >
                    <StepIcon step={step} />
                  </span>

                  <div className="min-w-0 flex-1">
                    <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                      <span className="min-w-0 truncate text-xs font-medium">{step.title}</span>
                      <span className="shrink-0 text-[10px] uppercase tracking-wide text-muted-foreground">{step.stage}</span>
                      {isIntegration && (
                        <Badge variant="outline" className="h-4 border-brand/35 px-1 text-[9px] text-brand">
                          <GitMerge className="size-2.5" aria-hidden />
                          {t(($) => $.detail.orchestration_integration_gate)}
                        </Badge>
                      )}
                      {step.squad_id && !isIntegration && (
                        <Badge variant="outline" className="h-4 px-1 text-[9px]">
                          <GitBranch className="size-2.5" aria-hidden />
                          {t(($) => $.detail.orchestration_squad_step)}
                        </Badge>
                      )}
                    </div>

                    <div className="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-muted-foreground">
                      {step.agent_id && <ActorAvatar actorType="agent" actorId={step.agent_id} size={18} enableHoverCard />}
                      {step.model && (
                        <code className="rounded bg-muted px-1 py-0.5 font-mono text-[10px]" translate="no">
                          {step.model}
                        </code>
                      )}
                      {step.merge_status === "clean" && (
                        <Badge variant="outline" className="h-4 border-success/40 px-1 text-[9px] text-success">
                          {t(($) => $.detail.orchestration_merge_clean)}
                        </Badge>
                      )}
                      {step.merge_status === "conflicts" && (
                        <Badge variant="destructive" className="h-4 px-1 text-[9px]">
                          {t(($) => $.detail.orchestration_conflicts, { count: step.conflict_files.length || 1 })}
                        </Badge>
                      )}
                      {step.merge_status === "uncommitted" && (
                        <Badge variant="outline" className="h-4 border-warning/40 px-1 text-[9px] text-warning">
                          {t(($) => $.detail.orchestration_uncommitted)}
                        </Badge>
                      )}
                      {isIntegration && step.integration_status === "pending" && (
                        <span>{t(($) => $.detail.orchestration_integration_pending)}</span>
                      )}
                      {isIntegration && step.integration_status === "complete" && (
                        <Badge variant="outline" className="h-4 border-success/40 px-1 text-[9px] text-success">
                          <Check className="size-2.5" aria-hidden />
                          {t(($) => $.detail.orchestration_heads_integrated, { count: step.integrated_head_shas.length })}
                        </Badge>
                      )}
                      {isIntegration && step.integration_status === "missing_heads" && (
                        <Badge variant="destructive" className="h-4 px-1 text-[9px]">
                          {t(($) => $.detail.orchestration_heads_missing, { count: step.missing_head_shas.length })}
                        </Badge>
                      )}
                      {isIntegration && step.integration_status === "conflicts" && (
                        <Badge variant="destructive" className="h-4 px-1 text-[9px]">
                          {t(($) => $.detail.orchestration_integration_conflicts)}
                        </Badge>
                      )}
                      {step.worktree_branch && (
                        <code className="max-w-56 truncate rounded bg-muted px-1 py-0.5 font-mono text-[9px]" translate="no">
                          {step.worktree_branch}
                        </code>
                      )}
                      {dependencyNames && (step.status === "pending" || isIntegration) && (
                        <span className="min-w-0 truncate">{t(($) => $.detail.orchestration_after, { steps: dependencyNames })}</span>
                      )}
                      {step.attempt > 0 && (
                        <span className="tabular-nums">{t(($) => $.detail.orchestration_attempt, { current: step.attempt, max: step.max_attempts })}</span>
                      )}
                      {step.error && <span className="min-w-0 break-words text-destructive">{step.error}</span>}
                    </div>
                  </div>
                </div>

                {step.status === "waiting_approval" && (
                  <div className="flex shrink-0 flex-wrap items-center gap-2 sm:justify-end">
                    <span className="flex items-center gap-1 text-[10px] font-medium text-success">
                      <Check className="size-3" aria-hidden />
                      {t(($) => $.detail.orchestration_ready_approval, { count: dependencies.length })}
                    </span>
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={approveStep.isPending}
                      onClick={() => approveStep.mutate({ issueId, stepId: step.id })}
                    >
                      {approveStep.isPending ? <Loader2 className="animate-spin motion-reduce:animate-none" aria-hidden /> : <ShieldCheck aria-hidden />}
                      {step.stage === "release" ? t(($) => $.detail.orchestration_approve_release) : t(($) => $.detail.orchestration_approve)}
                    </Button>
                  </div>
                )}
                {step.status === "failed" && step.attempt < step.max_attempts && (
                  <Button
                    size="sm"
                    variant="outline"
                    className="shrink-0"
                    disabled={retryStep.isPending}
                    onClick={() => retryStep.mutate({ issueId, stepId: step.id })}
                  >
                    {retryStep.isPending ? <Loader2 className="animate-spin motion-reduce:animate-none" aria-hidden /> : <RotateCcw aria-hidden />}
                    {t(($) => $.detail.orchestration_retry)}
                  </Button>
                )}
                {(["pending", "queued", "running"] as const).includes(step.status as "pending" | "queued" | "running") && (
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    className="shrink-0 self-end sm:self-auto"
                    disabled={cancelBranch.isPending}
                    onClick={() => cancelBranch.mutate({ issueId, stepId: step.id })}
                    aria-label={t(($) => $.detail.orchestration_cancel_branch, { title: step.title })}
                    title={t(($) => $.detail.orchestration_cancel_branch, { title: step.title })}
                  >
                    <Ban aria-hidden />
                  </Button>
                )}
              </div>
            </li>
          );
        })}
      </ol>

      {(events.length > 0 || run.revisions.length > 0) && (
        <details className="group border-t bg-muted/10">
          <summary className="flex cursor-pointer list-none items-center gap-2 px-4 py-3 text-xs font-medium text-muted-foreground hover:bg-muted/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring sm:px-5">
            <ChevronDown className="size-3.5 transition-transform group-open:rotate-180 motion-reduce:transition-none" aria-hidden />
            {t(($) => $.detail.orchestration_activity_history)}
            <Badge variant="secondary" className="ml-auto h-5 px-1.5 text-[9px] tabular-nums">
              {events.length + run.revisions.length}
            </Badge>
          </summary>
          <div className="grid gap-5 border-t px-4 py-3 sm:grid-cols-2 sm:px-5">
            {events.length > 0 && (
              <div className="min-w-0">
                <h3 className="mb-2 text-[10px] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
                  {t(($) => $.detail.orchestration_events)}
                </h3>
                <div className="space-y-1.5">
                  {events.map((event) => (
                    <div key={event.id} className="flex min-w-0 items-center gap-2 text-[11px] text-muted-foreground">
                      <Clock3 className="size-3 shrink-0" aria-hidden />
                      <span className="min-w-0 truncate">{eventLabel(event)}</span>
                      <time className="ml-auto shrink-0 tabular-nums" dateTime={event.created_at}>
                        {new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" }).format(new Date(event.created_at))}
                      </time>
                    </div>
                  ))}
                </div>
              </div>
            )}
            {run.revisions.length > 0 && (
              <div className="min-w-0">
                <h3 className="mb-2 text-[10px] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
                  {t(($) => $.detail.orchestration_plan_history)}
                </h3>
                {run.revisions.slice(0, 5).map((revision) => (
                  <div key={revision.id} className="flex min-w-0 gap-2 py-1 text-[11px] text-muted-foreground">
                    <span className="shrink-0 font-medium text-foreground tabular-nums">
                      {t(($) => $.detail.orchestration_version, { version: revision.version })}
                    </span>
                    <span className="min-w-0 truncate">{revision.reason}</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        </details>
      )}
    </section>
  );
}
