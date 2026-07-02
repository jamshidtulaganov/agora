"use client";

import { useEffect, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { TerminalSquare } from "lucide-react";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core/hooks";
import { useActorName } from "@agora/core/workspace/hooks";
import { agentTaskSnapshotOptions } from "@agora/core/agents";
import { taskMessagesOptions } from "@agora/core/chat/queries";
import type { AgentTask } from "@agora/core/types";
import type { TaskMessagePayload } from "@agora/core/types/events";
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

  // QA agents only — the review page's feed is about what QA is doing, so a
  // knowledge-capture (KB Synthesizer) or dev task that happens to be running
  // on the same issue must NOT clutter it. Resolve the QA squad's agent ids
  // (leader + agent members of any squad whose name contains "qa"). Cached; a
  // workspace with no QA squad yields an empty set → no filtering (show all),
  // so smaller setups still see their runs.
  const { data: qaAgentIds } = useQuery({
    queryKey: ["qa-squad-agent-ids", wsId],
    queryFn: async () => {
      const ids = new Set<string>();
      const squads = await api.listSquads();
      for (const s of squads) {
        if (!s.name?.toLowerCase().includes("qa")) continue;
        if (s.leader_id) ids.add(s.leader_id);
        try {
          for (const m of await api.listSquadMembers(s.id)) {
            if (m.member_type === "agent") ids.add(m.member_id);
          }
        } catch {
          // best-effort; leader alone still filters most noise
        }
      }
      return ids;
    },
    staleTime: 300_000,
  });

  const runningTasks = useMemo(
    () =>
      snapshot.filter(
        (task) =>
          task.issue_id === issueId &&
          task.status === "running" &&
          (!qaAgentIds || qaAgentIds.size === 0 || qaAgentIds.has(task.agent_id)),
      ),
    [snapshot, issueId, qaAgentIds],
  );

  if (runningTasks.length === 0) return null;

  return (
    <div className="flex flex-col gap-2">
      {runningTasks.map((task) => (
        <QATerminalPanel key={task.id} task={task} issueId={issueId} />
      ))}
    </div>
  );
}

// Jest-style "RUNS <spec>" — the run_test_cases instruction requires the agent
// to print `RUNNING test_case:<id>` immediately before driving each case's
// steps (slice_action.go, sliceActionRunTests). This is the ONLY place that
// signal is parsed; it's written into a small manually-managed query cache (no
// queryFn — mirrors the chatKeys.pendingTask pattern) so TestCasesPanel can
// read "which case is running right now" without itself subscribing to every
// running task's message stream.
const RUNNING_CASE_RE = /RUNNING test_case:([0-9a-fA-F-]{8,})/;
// Per-case live verdict — the run_test_cases recipe requires the agent (or its
// compiled script) to print `QA_RESULT test_case:<id> pass|fail` the instant a
// case finishes, so the panel flips that row's ✓/✗ live DURING the run instead
// of only after the whole run persists its test_run rows at the end.
const CASE_RESULT_RE = /QA_RESULT test_case:([0-9a-fA-F-]{8,})\s+(pass|fail)/gi;

function runningCaseQueryKey(issueId: string) {
  return ["qa-running-case", issueId] as const;
}
function caseVerdictsQueryKey(issueId: string) {
  return ["qa-live-case-verdicts", issueId] as const;
}

export function useRunningTestCaseId(issueId: string): string | null {
  const { data } = useQuery({
    queryKey: runningCaseQueryKey(issueId),
    queryFn: () => null as string | null,
    enabled: false,
    initialData: null,
    staleTime: Infinity,
  });
  return data ?? null;
}

// Live per-case verdicts parsed from the running stream (id → "pass"|"fail").
// Empty until the first QA_RESULT marker; cleared when the run's task leaves the
// running set (the persisted test_run rows take over as the source of truth).
export function useLiveCaseVerdicts(issueId: string): Record<string, "pass" | "fail"> {
  const { data } = useQuery({
    queryKey: caseVerdictsQueryKey(issueId),
    queryFn: () => ({}) as Record<string, "pass" | "fail">,
    enabled: false,
    initialData: {} as Record<string, "pass" | "fail">,
    staleTime: Infinity,
  });
  return data ?? {};
}

function extractRunningCaseId(messages: TaskMessagePayload[]): string | null {
  let found: string | null = null;
  for (const m of messages) {
    if (!m.content) continue;
    const match = m.content.match(RUNNING_CASE_RE);
    // A later message's marker always wins — messages arrive seq-ordered, so
    // the last match across the whole stream is the most recent one.
    if (match) found = match[1]!;
  }
  return found;
}

function extractCaseVerdicts(messages: TaskMessagePayload[]): Record<string, "pass" | "fail"> {
  const out: Record<string, "pass" | "fail"> = {};
  for (const m of messages) {
    if (!m.content) continue;
    for (const match of m.content.matchAll(CASE_RESULT_RE)) {
      // A later marker for the same case wins (a re-run within the same stream).
      out[match[1]!] = match[2]!.toLowerCase() as "pass" | "fail";
    }
  }
  return out;
}

function QATerminalPanel({ task, issueId }: { task: AgentTask; issueId: string }) {
  const { t } = useT("issues");
  const { getActorName } = useActorName();
  const qc = useQueryClient();
  const { data: messages = [] } = useQuery(taskMessagesOptions(task.id));

  const timeline = useMemo(() => buildTimeline(messages), [messages]);
  const steps = useMemo(() => deriveActivitySteps(timeline), [timeline]);
  const runningCaseId = useMemo(() => extractRunningCaseId(messages), [messages]);
  const caseVerdicts = useMemo(() => extractCaseVerdicts(messages), [messages]);

  useEffect(() => {
    qc.setQueryData(runningCaseQueryKey(issueId), runningCaseId);
    return () => {
      // Clear on unmount (task left the running set) — but only if nothing
      // newer already overwrote it, so a second concurrent QA task's marker
      // (or a fresh run) is never clobbered by this panel's own teardown.
      qc.setQueryData(runningCaseQueryKey(issueId), (old: string | null | undefined) =>
        old === runningCaseId ? null : old,
      );
    };
  }, [qc, issueId, runningCaseId]);

  useEffect(() => {
    // Merge this task's parsed verdicts into the shared live map (never clobber
    // another concurrent QA task's entries). Cleared on unmount so the persisted
    // test_run rows become the source of truth once the run ends.
    if (Object.keys(caseVerdicts).length > 0) {
      qc.setQueryData(caseVerdictsQueryKey(issueId), (old: Record<string, "pass" | "fail"> | undefined) => ({
        ...(old ?? {}),
        ...caseVerdicts,
      }));
    }
    return () => {
      qc.setQueryData(caseVerdictsQueryKey(issueId), {} as Record<string, "pass" | "fail">);
    };
  }, [qc, issueId, caseVerdicts]);

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
