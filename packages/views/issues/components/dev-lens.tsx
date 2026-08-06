"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowLeft,
  CheckCircle2,
  ChevronDown,
  Circle,
  Clock3,
  Eye,
  FileDiff,
  Loader2,
  OctagonAlert,
  Radio,
  ShieldCheck,
} from "lucide-react";
import { api } from "@agora/core/api";
import {
  issueArtifactOptions,
  issueKeys,
  issueOrchestrationOptions,
} from "@agora/core/issues/queries";
import type { AgentTask, OrchestrationStep } from "@agora/core/types";
import { useActorName } from "@agora/core/workspace/hooks";
import { Badge } from "@agora/ui/components/ui/badge";
import { Button } from "@agora/ui/components/ui/button";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@agora/ui/components/ui/tabs";
import { cn } from "@agora/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n";
import { useNavigation } from "../../navigation";
import { ArtifactCodeViewer } from "./artifact-code-viewer";
import { ArtifactChecksPanel, ArtifactPreviewPanel } from "./artifact-runtime-panels";
import {
  orchestrationDisplayGroups,
  orchestrationHandoffText,
  orchestrationStepDisplayTitle,
  type OrchestrationDisplayKind,
} from "./orchestration-display";
import { StageLiveProcessBody } from "./stage-live-process";

const DEV_TAB_QUERY_KEY = "dev_tab";
const DEV_TABS = ["activity", "changes", "preview", "checks"] as const;
type DevTab = (typeof DEV_TABS)[number];

function isDevTab(value: string | null): value is DevTab {
  return DEV_TABS.some((tab) => tab === value);
}

type IssuesT = ReturnType<typeof useT<"issues">>["t"];

function activityStageLabel(kind: OrchestrationDisplayKind, t: IssuesT): string {
  switch (kind) {
    case "plan": return t(($) => $.execution_surface.stage_plan);
    case "dev": return t(($) => $.execution_surface.stage_dev);
    case "integration": return t(($) => $.execution_surface.stage_integration);
    case "qa": return t(($) => $.execution_surface.stage_qa);
    case "review": return t(($) => $.execution_surface.stage_review);
    case "release": return t(($) => $.execution_surface.stage_release);
  }
}

function activeTask(status?: AgentTask["status"] | OrchestrationStep["status"]): boolean {
  return status === "running" || status === "queued" || status === "dispatched" || status === "waiting_local_directory";
}

function statusMeta(status?: AgentTask["status"] | OrchestrationStep["status"]) {
  if (status === "completed") return { Icon: CheckCircle2, tone: "text-success", key: "completed" as const };
  if (status === "failed" || status === "cancelled") return { Icon: OctagonAlert, tone: "text-destructive", key: "failed" as const };
  if (status === "running") return { Icon: Loader2, tone: "text-brand", key: "running" as const, spin: true };
  if (status === "queued" || status === "dispatched" || status === "waiting_local_directory") {
    return { Icon: Clock3, tone: "text-warning", key: "queued" as const };
  }
  return { Icon: Circle, tone: "text-muted-foreground", key: "waiting" as const };
}

// Completed model output contains the live PROGRESS stream as well as the
// actual handoff. Activity keeps the useful final paragraphs and leaves the
// full transcript in the issue conversation, avoiding a second chat surface.
function handoffSummary(value: unknown): string {
  const cleaned = orchestrationHandoffText(value);
  if (!cleaned) return "";
  const paragraphs = cleaned.split(/\n\s*\n/).map((part) => part.trim()).filter(Boolean);
  return paragraphs.slice(-2).join("\n\n");
}

// A step brief or handoff can be arbitrarily long (agents write a lot).
// Clamp by default and reveal on demand so large plans stay scannable.
const CLAMP_THRESHOLD_CHARS = 280;
const CLAMP_THRESHOLD_LINES = 4;

function ClampedText({ text, clampClass }: { text: string; clampClass: string }) {
  const { t } = useT("issues");
  const [expanded, setExpanded] = useState(false);
  const long = text.length > CLAMP_THRESHOLD_CHARS || text.split("\n").length > CLAMP_THRESHOLD_LINES;
  return (
    <div className="min-w-0">
      <p className={cn("whitespace-pre-wrap leading-relaxed text-foreground/80", !expanded && long && clampClass)}>
        {text}
      </p>
      {long && (
        <button
          type="button"
          className="mt-1 text-[11px] font-medium text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
          onClick={() => setExpanded((value) => !value)}
        >
          {expanded ? t(($) => $.dev_workspace.show_less) : t(($) => $.dev_workspace.show_more)}
        </button>
      )}
    </div>
  );
}

function DevActivityCard({
  step,
  task,
  instruction,
  defaultOpen,
}: {
  step: OrchestrationStep;
  task?: AgentTask;
  instruction?: string;
  defaultOpen: boolean;
}) {
  const { t } = useT("issues");
  const { getAgentName } = useActorName();
  const [open, setOpen] = useState(defaultOpen);
  const agentId = step.agent_id ?? task?.agent_id ?? "";
  const status = step.status;
  const meta = statusMeta(status);
  const title = orchestrationStepDisplayTitle(step);
  const sha = step.head_sha;
  const failed = status === "failed" || status === "cancelled";
  const handoff = handoffSummary(step.output ?? task?.result);

  const labels = {
    completed: t(($) => $.dev_workspace.status_completed),
    failed: t(($) => $.dev_workspace.status_failed),
    running: t(($) => $.dev_workspace.status_running),
    queued: t(($) => $.dev_workspace.status_queued),
    waiting: t(($) => $.dev_workspace.status_waiting),
  };

  return (
    <article className="min-w-0 overflow-hidden rounded-lg border bg-background">
      {/* The whole header is the expand/collapse control so a large persisted
          plan scans like a checklist and opens only on demand. */}
      <button
        type="button"
        className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-muted/30"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        {agentId ? <ActorAvatar actorType="agent" actorId={agentId} size={24} /> : <span className="flex size-6 shrink-0 items-center justify-center rounded-md border bg-muted"><Radio className="size-3" aria-hidden /></span>}
        <span className="min-w-0 flex-1">
          <span className="block truncate text-xs font-semibold" title={title}>{title}</span>
          <span className="block truncate text-[11px] text-muted-foreground">
            {agentId ? getAgentName(agentId) : t(($) => $.dev_workspace.unassigned_worker)}
          </span>
        </span>
        {!open && status === "completed" && sha && (
          <span className="hidden items-center gap-1 font-mono text-[10px] text-muted-foreground sm:flex">
            {sha.slice(0, 8)}
          </span>
        )}
        <Badge variant="outline" className={cn("shrink-0 font-normal", meta.tone)}>
          <meta.Icon className={cn(meta.spin && "animate-spin motion-reduce:animate-none")} aria-hidden />
          {labels[meta.key]}
        </Badge>
        <ChevronDown className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", open && "rotate-180")} aria-hidden />
      </button>
      {open && (
        <div className="border-t">
          {instruction && (
            <div className="border-b bg-muted/20 px-3 py-2.5 text-xs">
              <p className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
                {t(($) => $.dev_workspace.task_brief)}
              </p>
              <div className="mt-1">
                <ClampedText text={instruction} clampClass="line-clamp-3" />
              </div>
            </div>
          )}
          <div className="p-3">
            {task && activeTask(status) ? (
              <StageLiveProcessBody taskId={task.id} />
            ) : status === "completed" ? (
              <div className="space-y-2 text-xs">
                <div className="flex items-center gap-1.5 text-success">
                  <CheckCircle2 className="size-3.5" aria-hidden />
                  <span className="font-medium">
                    {handoff
                      ? t(($) => $.dev_workspace.handoff_complete)
                      : t(($) => $.dev_workspace.status_completed)}
                  </span>
                </div>
                {handoff && (
                  <>
                    <p className="text-muted-foreground">{t(($) => $.dev_workspace.handoff_complete_description)}</p>
                    <div className="rounded-md border bg-muted/20 px-2.5 py-2">
                      <p className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
                        {t(($) => $.dev_workspace.agent_handoff)}
                      </p>
                      <div className="mt-1">
                        <ClampedText text={handoff} clampClass="line-clamp-6" />
                      </div>
                    </div>
                  </>
                )}
                {sha && (
                  <div className="flex items-center gap-2 rounded-md bg-muted/40 px-2 py-1.5 font-mono text-[11px]">
                    <span className="text-muted-foreground">HEAD</span>
                    <span>{sha.slice(0, 8)}</span>
                    {step.merge_status === "clean" && <span className="ml-auto text-success">{t(($) => $.dev_workspace.clean)}</span>}
                  </div>
                )}
              </div>
            ) : failed ? (
              <div className="flex items-start gap-2 text-xs text-destructive">
                <OctagonAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden />
                <p className="min-w-0">{t(($) => $.dev_workspace.worker_failed_description)}</p>
              </div>
            ) : (
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <Clock3 className="size-3.5" aria-hidden />
                {t(($) => $.dev_workspace.waiting_for_worker)}
              </div>
            )}
          </div>
        </div>
      )}
    </article>
  );
}

export function DevActivity({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const { data: run, isLoading: runLoading } = useQuery(issueOrchestrationOptions(issueId));
  const { data: tasks = [], isLoading: tasksLoading } = useQuery({
    queryKey: issueKeys.tasks(issueId),
    queryFn: () => api.listTasksByIssue(issueId),
    staleTime: 30_000,
    enabled: !!run,
  });
  const taskById = useMemo(() => new Map(tasks.map((task) => [task.id, task])), [tasks]);
  const groups = useMemo(
    () => orchestrationDisplayGroups(run?.steps ?? []),
    [run?.steps],
  );
  const lanes = groups.flatMap((group) => group.steps.map((step) => ({
    step,
    task: step.task_id ? taskById.get(step.task_id) : undefined,
  })));
  const working = lanes.filter(({ step }) => activeTask(step.status)).length;
  const complete = lanes.filter(({ step }) => step.status === "completed").length;

  if (runLoading || (!!run && tasksLoading)) {
    return <div className="h-80 animate-pulse rounded-lg border bg-muted/30 motion-reduce:animate-none" aria-hidden />;
  }
  if (lanes.length === 0) {
    return (
      <div className="flex min-h-80 items-center justify-center rounded-lg border border-dashed bg-muted/10 px-6 text-center">
        <div className="max-w-sm">
          <Radio className="mx-auto size-5 text-muted-foreground" aria-hidden />
          <h3 className="mt-3 text-sm font-semibold">{t(($) => $.dev_workspace.activity_empty)}</h3>
          <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.dev_workspace.activity_empty_description)}</p>
        </div>
      </div>
    );
  }

  return (
    <section aria-label={t(($) => $.dev_workspace.activity)}>
      <div className="mb-3 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span>{t(($) => $.dev_workspace.step_count, { count: lanes.length })}</span>
        {working > 0 && (
          <span className="inline-flex items-center gap-1.5 text-brand">
            <span className="size-1.5 rounded-full bg-brand motion-safe:animate-pulse" aria-hidden />
            {t(($) => $.dev_workspace.working_count, { count: working })}
          </span>
        )}
        {complete > 0 && <span>· {t(($) => $.dev_workspace.completed_count, { count: complete })}</span>}
      </div>
      <div className="space-y-5">
        {groups.map((group) => (
          <section key={group.kind} aria-labelledby={`orchestration-group-${group.kind}`}>
            <div className="mb-2 flex items-center gap-2">
              <h3 id={`orchestration-group-${group.kind}`} className="text-[11px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
                {activityStageLabel(group.kind, t)}
              </h3>
              {group.steps.length > 1 && (
                <span className="font-mono text-[10px] tabular-nums text-muted-foreground">{group.steps.length}</span>
              )}
            </div>
            <div className={cn("grid gap-2", group.kind === "dev" && group.steps.length > 1 && "md:grid-cols-2")}>
              {group.steps.map((step) => {
                const task = step.task_id ? taskById.get(step.task_id) : undefined;
                const inMotion = step.status === "running" || step.status === "queued";
                return (
                  <DevActivityCard
                    key={step.id}
                    step={step}
                    task={task}
                    instruction={step.instructions.trim() || task?.trigger_summary?.trim() || undefined}
                    defaultOpen={lanes.length <= 2 || inMotion || step.status === "failed" || step.status === "cancelled"}
                  />
                );
              })}
            </div>
          </section>
        ))}
      </div>
    </section>
  );
}

export function DevLensBody({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const navigation = useNavigation();
  const requested = navigation.searchParams.get(DEV_TAB_QUERY_KEY);
  const activeTab: DevTab = isDevTab(requested) ? requested : "activity";
  const { data: run } = useQuery(issueOrchestrationOptions(issueId));
  const { data: artifactData } = useQuery(issueArtifactOptions(issueId));
  const activeSteps = (run?.steps ?? []).filter((step) => activeTask(step.status)).length;

  const setTab = (value: string) => {
    if (!isDevTab(value)) return;
    const params = new URLSearchParams(navigation.searchParams);
    params.set(DEV_TAB_QUERY_KEY, value);
    navigation.replace(`${navigation.pathname}?${params.toString()}`);
  };

  const backToIssue = () => {
    const params = new URLSearchParams(navigation.searchParams);
    params.delete("lens");
    params.delete(DEV_TAB_QUERY_KEY);
    const query = params.toString();
    navigation.replace(query ? `${navigation.pathname}?${query}` : navigation.pathname);
  };

  return (
    <div className="flex h-[calc(100vh-6rem)] min-h-0 w-full flex-col px-4 py-4">
      <Tabs value={activeTab} onValueChange={setTab} className="min-h-0 flex-1 gap-3">
        <div className="flex min-w-0 items-center gap-3 border-b">
          <TabsList
            variant="line"
            className="no-scrollbar min-w-0 max-w-full justify-start overflow-x-auto"
          >
            <TabsTrigger value="activity">
              <Radio aria-hidden />
              {t(($) => $.dev_workspace.activity)}
              {activeSteps > 0 && <span className="size-1.5 rounded-full bg-brand motion-safe:animate-pulse" aria-label={t(($) => $.dev_workspace.workers_active)} />}
            </TabsTrigger>
            <TabsTrigger value="changes">
              <FileDiff aria-hidden />
              {t(($) => $.dev_workspace.changes)}
              {artifactData?.artifact && <CheckCircle2 className="text-success" aria-label={t(($) => $.dev_workspace.artifact_ready)} />}
            </TabsTrigger>
            <TabsTrigger value="preview">
              <Eye aria-hidden />
              {t(($) => $.dev_workspace.preview)}
            </TabsTrigger>
            <TabsTrigger value="checks">
              <ShieldCheck aria-hidden />
              {t(($) => $.dev_workspace.checks)}
            </TabsTrigger>
          </TabsList>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="ml-auto shrink-0"
            onClick={backToIssue}
          >
            <ArrowLeft aria-hidden />
            <span className="hidden sm:inline">{t(($) => $.dev_workspace.back_to_issue)}</span>
          </Button>
        </div>
        <TabsContent value="activity" className="min-h-0 overflow-auto">
          <DevActivity issueId={issueId} />
        </TabsContent>
        <TabsContent value="changes" className="min-h-0 overflow-auto">
          <ArtifactCodeViewer issueId={issueId} className="min-h-[34rem]" />
        </TabsContent>
        <TabsContent value="preview" className="min-h-0 overflow-auto">
          <ArtifactPreviewPanel issueId={issueId} />
        </TabsContent>
        <TabsContent value="checks" className="min-h-0 overflow-auto">
          <ArtifactChecksPanel issueId={issueId} />
        </TabsContent>
      </Tabs>
    </div>
  );
}
