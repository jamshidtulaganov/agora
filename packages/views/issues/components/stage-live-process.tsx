"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { CheckCircle2, Circle, FilePenLine, FilePlus2, Loader2 } from "lucide-react";
import { taskMessagesOptions } from "@agora/core/chat/queries";
import type { StagePipeline } from "@agora/core/issues";
import { cn } from "@agora/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { buildTimeline } from "../../common/task-transcript";
import { useT } from "../../i18n";
import { OrchestratorNarrative } from "./orchestrator-narrative";
import {
  deriveFileChanges,
  deriveMilestoneSteps,
  deriveProgressHeadline,
  deriveTodos,
  type ActivityStep,
  type TodoItem,
} from "./live-agent-activity";

// ─────────────────────────────────────────────────────────────────────────────
// Binds an SDLC stage to the LIVE process of the agent running it. Same data
// pipeline the buried live feed uses — `taskMessagesOptions(taskId)` streams
// `task:message` events into a React-Query cache (use-realtime-sync.ts) and
// `buildTimeline` → `deriveActivitySteps`/`deriveFileChanges` turn them into a
// readable step trail + git-style changeset. Here that stream is surfaced right
// on the stepper: the trailing slot shows the newest step ("editing Button.tsx",
// "running the tests") and the stage's watch popover shows the recent trail.
//
// No new endpoint, no new WS event: the stepper just reads the same live cache
// the transcript modal does, keyed by the task id the pipeline now attributes
// to each stage (packages/core/issues/stage.ts, use-stage-pipeline.ts).
// ─────────────────────────────────────────────────────────────────────────────

/**
 * Localize one activity step into a single "what the agent is doing" phrase.
 * A classified shell command reads as its human intent ("running the tests");
 * otherwise it's "{verb} {target}" ("is editing src/Button.tsx"). Shared by the
 * stepper's live surfaces and the buried changes feed so they read identically.
 */
export function useStepText(step: ActivityStep): string {
  const { t } = useT("issues");
  let verb = "";
  switch (step.verbKey) {
    case "reading": verb = t(($) => $.live_activity.verb.reading); break;
    case "editing": verb = t(($) => $.live_activity.verb.editing); break;
    case "writing": verb = t(($) => $.live_activity.verb.writing); break;
    case "searching": verb = t(($) => $.live_activity.verb.searching); break;
    case "running": verb = t(($) => $.live_activity.verb.running); break;
    case "fetching": verb = t(($) => $.live_activity.verb.fetching); break;
    case "browsing": verb = t(($) => $.live_activity.verb.browsing); break;
    case "thinking": verb = t(($) => $.live_activity.verb.thinking); break;
    case "working": verb = t(($) => $.live_activity.verb.working); break;
    default: verb = step.rawVerb ?? "";
  }
  let human = "";
  switch (step.cmdClass) {
    case "install": human = t(($) => $.live_activity.cmd.install); break;
    case "test": human = t(($) => $.live_activity.cmd.test); break;
    case "lint": human = t(($) => $.live_activity.cmd.lint); break;
    case "build": human = t(($) => $.live_activity.cmd.build); break;
    case "review": human = t(($) => $.live_activity.cmd.review); break;
    case "branch": human = t(($) => $.live_activity.cmd.branch); break;
    case "commit": human = t(($) => $.live_activity.cmd.commit); break;
    case "publish": human = t(($) => $.live_activity.cmd.publish); break;
    case "pr": human = t(($) => $.live_activity.cmd.pr); break;
    case "inspect": human = t(($) => $.live_activity.cmd.inspect); break;
    default: break;
  }
  return human || (step.target ? `${verb} ${step.target}` : verb);
}

// Subscribe a single task's live message cache and derive its step trail +
// changeset. Encapsulates the shared plumbing so the surfaces below only render.
// Exported for the issue plan panel's solo (undecomposed) fallback.
export function useTaskLive(taskId: string) {
  const { data: messages = [] } = useQuery(taskMessagesOptions(taskId));
  const timeline = useMemo(() => buildTimeline(messages), [messages]);
  const steps = useMemo(() => deriveMilestoneSteps(timeline), [timeline]);
  const changes = useMemo(() => deriveFileChanges(timeline), [timeline]);
  const todos = useMemo(() => deriveTodos(timeline), [timeline]);
  const headline = useMemo(() => deriveProgressHeadline(timeline), [timeline]);
  const latestMessageAt = useMemo(() => {
    for (let index = messages.length - 1; index >= 0; index -= 1) {
      const message = messages[index];
      if (message?.created_at) return message.created_at;
    }
    return undefined;
  }, [messages]);
  return { steps, changes, todos, headline, latestMessageAt };
}

// ─── To-do list: the agent's own plan + "now doing" ─────────────────────────

/**
 * Presentational agent to-do list. Completed items check off, the in-progress
 * item is highlighted as "now" (its active-form phrase, with a spinner), and
 * pending items stay dim — so a human reads what's done, what's happening now,
 * and what's left, in the agent's own words. Renders nothing for an empty list.
 */
export function TodoList({ todos }: { todos: TodoItem[] }) {
  const { t } = useT("issues");
  if (todos.length === 0) return null;
  const done = todos.filter((td) => td.status === "completed").length;

  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center justify-between text-[11px] font-medium text-muted-foreground">
        <span>{t(($) => $.live_activity.todo_title)}</span>
        <span className="font-mono tabular-nums">
          {t(($) => $.live_activity.todo_progress, { done, total: todos.length })}
        </span>
      </div>
      <ul className="flex flex-col gap-0.5">
        {todos.map((td, i) => (
          <TodoRow key={i} todo={td} />
        ))}
      </ul>
    </div>
  );
}

function TodoRow({ todo }: { todo: TodoItem }) {
  const running = todo.status === "in_progress";
  const done = todo.status === "completed";
  const text = running ? (todo.activeForm ?? todo.content) : todo.content;
  return (
    <li className="flex items-start gap-1.5 text-[11px]" aria-current={running || undefined}>
      {done ? (
        <CheckCircle2 aria-hidden className="mt-px size-3 shrink-0 text-success" />
      ) : running ? (
        <Loader2 aria-hidden className="mt-px size-3 shrink-0 animate-spin text-info" />
      ) : (
        <Circle aria-hidden className="mt-px size-3 shrink-0 text-muted-foreground/40" />
      )}
      <span
        className={cn(
          "min-w-0 flex-1",
          done && "text-muted-foreground/60 line-through",
          running && "font-medium text-foreground",
          !done && !running && "text-muted-foreground",
        )}
      >
        {text}
      </span>
    </li>
  );
}

// ─── Trailing slot: one live line for the current stage ─────────────────────

/**
 * The stepper's trailing narrative when the current stage has a running task:
 * the newest activity step, live. Falls back to a neutral "working…" until the
 * first tool call streams. Prefixes the orchestrator's avatar when there is
 * one (mirrors the static OrchestratorNarrative it replaces).
 */
function StageLiveActivity({
  taskId,
  orchestratorAgentId,
}: {
  taskId: string;
  orchestratorAgentId: string | null | undefined;
}) {
  const { t } = useT("issues");
  const { steps, todos, headline } = useTaskLive(taskId);
  const newest = steps.at(-1);
  const stepText = useStepText(newest ?? { key: "", verbKey: "working" });

  // Priority of the live line, most-human first: the agent's own PROGRESS
  // headline → its in-progress to-do → a derived milestone step → "working…".
  const current = todos.find((td) => td.status === "in_progress");
  const label =
    headline ??
    (current
      ? (current.activeForm ?? current.content)
      : newest
        ? stepText
        : t(($) => $.live_activity.working));

  return (
    <span className="flex min-w-0 items-center gap-1.5 whitespace-nowrap text-muted-foreground">
      {orchestratorAgentId && (
        <ActorAvatar actorType="agent" actorId={orchestratorAgentId} size={14} />
      )}
      <span
        aria-hidden
        className="size-1.5 shrink-0 rounded-full bg-info motion-safe:animate-pulse"
      />
      <span className="min-w-0 max-w-[16rem] truncate text-foreground" aria-live="polite">
        {label}
      </span>
    </span>
  );
}

/**
 * Trailing content for the SDLC stepper. Shows the live newest step when the
 * current stage has a running task; otherwise the static orchestrator phrase.
 * Drop-in replacement for the bare <OrchestratorNarrative> the stepper used.
 */
export function StageTrailing({
  pipeline,
  orchestratorAgentId,
}: {
  pipeline: StagePipeline;
  orchestratorAgentId: string | null | undefined;
}) {
  const current = pipeline.stages.find((s) => s.stage === pipeline.current);
  if (current?.taskId) {
    return (
      <StageLiveActivity
        taskId={current.taskId}
        orchestratorAgentId={orchestratorAgentId}
      />
    );
  }
  return (
    <OrchestratorNarrative
      pipeline={pipeline}
      orchestratorAgentId={orchestratorAgentId}
    />
  );
}

// ─── Watch popover: the recent process for a stage's task ────────────────────

const MAX_STEPS = 7;
const MAX_FILES = 5;

/**
 * The live "what the agent is doing now" body shown when a running stage is
 * clicked. A newest-first step trail plus, when the run has written files, a
 * compact changed-files summary. Read-only: subscribes the same live cache as
 * the trailing line, so it stays in lockstep with the stepper.
 */
export function StageLiveProcessBody({ taskId }: { taskId: string }) {
  const { t } = useT("issues");
  const { steps, changes, todos, headline } = useTaskLive(taskId);

  // Newest first, capped — a long run stays glanceable.
  const recent = useMemo(() => [...steps].reverse().slice(0, MAX_STEPS), [steps]);
  const files = changes.slice(0, MAX_FILES);
  const hiddenFiles = changes.length - files.length;

  return (
    <div className="flex flex-col gap-2 text-xs">
      {/* The agent's own plan leads — what's done, what's now, what's left. */}
      {todos.length > 0 && (
        <>
          <TodoList todos={todos} />
          {(recent.length > 0 || files.length > 0) && (
            <div className="border-t" />
          )}
        </>
      )}

      {/* The agent's own PROGRESS headline is the "now"; fall back to a static
          label when it hasn't emitted one. */}
      <div className="flex items-center gap-1.5">
        <span
          aria-hidden
          className="size-1.5 shrink-0 rounded-full bg-info motion-safe:animate-pulse"
        />
        <span className={cn("font-medium", headline ? "text-foreground" : "text-muted-foreground")}>
          {headline ?? t(($) => $.live_activity.now_title)}
        </span>
      </div>

      {recent.length > 0 ? (
        <ul className="flex flex-col gap-0.5">
          {recent.map((step) => (
            <StepLine key={step.key} step={step} />
          ))}
        </ul>
      ) : (
        <span className="text-muted-foreground">{t(($) => $.live_activity.working)}</span>
      )}

      {files.length > 0 && (
        <div className="flex flex-col gap-0.5 border-t pt-2">
          <span className="text-[11px] font-medium text-muted-foreground">
            {t(($) => $.live_activity.files, { count: changes.length })}
          </span>
          {files.map((c) => {
            const Icon = c.kind === "write" ? FilePlus2 : FilePenLine;
            return (
              <div key={c.key} className="flex items-center gap-1.5" title={c.path}>
                <Icon aria-hidden className="size-3 shrink-0 text-muted-foreground/70" />
                <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-foreground/80">
                  {c.shortPath}
                </span>
                <span className="shrink-0 font-mono text-[10.5px] tabular-nums">
                  <span className={c.additions > 0 ? "text-emerald-500" : "text-muted-foreground/40"}>
                    +{c.additions}
                  </span>{" "}
                  <span className={c.deletions > 0 ? "text-destructive" : "text-muted-foreground/40"}>
                    −{c.deletions}
                  </span>
                </span>
              </div>
            );
          })}
          {hiddenFiles > 0 && (
            <span className="text-[11px] text-muted-foreground/70">
              {t(($) => $.live_activity.more, { count: hiddenFiles })}
            </span>
          )}
        </div>
      )}
    </div>
  );
}

function StepLine({ step }: { step: ActivityStep }) {
  const text = useStepText(step);
  return (
    <li className="flex items-center gap-1.5">
      <span aria-hidden className="size-1 shrink-0 rounded-full bg-muted-foreground/40" />
      <span className={cn("min-w-0 flex-1 truncate text-[11px] text-muted-foreground")} title={text}>
        {text}
      </span>
    </li>
  );
}
