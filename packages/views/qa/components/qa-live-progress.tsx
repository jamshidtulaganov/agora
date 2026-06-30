"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { TerminalSquare } from "lucide-react";
import { useWorkspaceId } from "@agora/core/hooks";
import { useActorName } from "@agora/core/workspace/hooks";
import { agentTaskSnapshotOptions } from "@agora/core/agents";
import { taskMessagesOptions } from "@agora/core/chat/queries";
import type { AgentTask } from "@agora/core/types";
import { buildTimeline } from "../../common/task-transcript";
import { deriveActivitySteps, type ActivityStep } from "../../issues/components/live-agent-activity";
import { useT } from "../../i18n";

// Terminal-style live progress for the QA review page: while run_qa /
// run_test_cases is actively running on this issue, show what the agent is
// DOING right now (the commands/checks it's executing), step by step, the way
// a senior QA engineer watching a CI log would. Reuses the exact pipeline
// LiveAgentChangesFeed already proved on the issue detail page — task:message
// → ["task-messages", taskId] cache → buildTimeline → deriveActivitySteps —
// just without that component's trigger-comment filtering (the QA page wants
// EVERY running task on the issue, not just comment-triggered ones, since
// run_qa/run_test_cases are slice actions that always carry a trigger comment
// but the page shouldn't have to know that to show them).
//
// Renders nothing when no agent is running (no layout shift in the idle case)
// — same contract as LiveAgentChangesFeed.

const MAX_VISIBLE_STEPS = 12;

export function QALiveProgress({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceId();
  const { data: snapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));

  const runningTasks = useMemo(
    () => snapshot.filter((task) => task.issue_id === issueId && task.status === "running"),
    [snapshot, issueId],
  );

  if (runningTasks.length === 0) return null;

  return (
    <div className="flex flex-col gap-2">
      {runningTasks.map((task) => (
        <QATerminalPanel key={task.id} task={task} />
      ))}
    </div>
  );
}

function QATerminalPanel({ task }: { task: AgentTask }) {
  const { t } = useT("issues");
  const { getActorName } = useActorName();
  const { data: messages = [] } = useQuery(taskMessagesOptions(task.id));

  const timeline = useMemo(() => buildTimeline(messages), [messages]);
  const steps = useMemo(() => deriveActivitySteps(timeline), [timeline]);
  const agentName = getActorName("agent", task.agent_id);
  // Newest-first feed, like a terminal scrolling — the latest command is the
  // one a reviewer cares about ("what is it doing RIGHT NOW").
  const visible = steps.slice(0, MAX_VISIBLE_STEPS);

  return (
    <div className="rounded-md border bg-muted/30 text-xs" aria-live="polite">
      <div className="flex items-center gap-2 border-b px-2.5 py-1.5">
        <TerminalSquare className="size-3.5 shrink-0 text-muted-foreground" />
        <span
          aria-hidden
          className="size-1.5 shrink-0 rounded-full bg-info motion-safe:animate-pulse"
        />
        <span className="min-w-0 flex-1 truncate text-muted-foreground">
          <span className="font-medium text-foreground/80">{agentName}</span>
          <span className="mx-1 text-muted-foreground/50">·</span>
          {visible.length === 0
            ? t(($) => $.live_activity.working)
            : t(($) => $.live_activity.files, { count: visible.length })}
        </span>
      </div>
      {visible.length > 0 ? (
        <ul className="flex max-h-48 flex-col overflow-y-auto font-mono">
          {visible.map((step) => (
            <TerminalStepRow key={step.key} step={step} />
          ))}
        </ul>
      ) : (
        <div className="px-2.5 py-2 text-[11px] text-muted-foreground/70">
          {t(($) => $.live_activity.working)}…
        </div>
      )}
    </div>
  );
}

function TerminalStepRow({ step }: { step: ActivityStep }) {
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
  const text = step.target ? `${verb} ${step.target}` : verb;
  return (
    <li
      className="flex items-baseline gap-1.5 px-2.5 py-1 text-[11px] text-muted-foreground/90"
      title={text}
    >
      <span aria-hidden className="shrink-0 text-emerald-600/70 dark:text-emerald-400/70">
        $
      </span>
      <span className="min-w-0 flex-1 truncate">{text}</span>
    </li>
  );
}
