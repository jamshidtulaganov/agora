"use client";

import { useEffect, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { FlaskConical, CheckCircle2, XCircle, Loader2 } from "lucide-react";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core/hooks";
import { agentTaskSnapshotOptions } from "@agora/core/agents";
import { taskMessagesOptions } from "@agora/core/chat/queries";
import { testCasesOptions } from "@agora/core/issues/queries";
import type { AgentTask } from "@agora/core/types";
import type { TaskMessagePayload } from "@agora/core/types/events";
import { useT } from "../../i18n";

// QA run status for the review page. Deliberately NOT a tool-call terminal —
// a QA reviewer does not want the agent's raw `mcp__…`/shell tool log. They
// want two things, and those live elsewhere: (1) WATCH the run — the live
// browser pane below (the agent shares that exact Chromium over CDP during a
// scripted run); (2) WHICH test case is running + its verdict — the Test-cases
// panel, plus the slim strip this renders above the browser so it's visible
// without opening the rail.
//
// This component's real job is двоякое: render that slim status strip, AND keep
// the run markers fresh for the Test-cases panel. The agent prints
// `RUNNING test_case:<id>` before each case and `QA_RESULT test_case:<id>
// pass|fail` the moment it finishes; a headless per-task watcher parses those
// from the live stream into small query caches (below). No tool calls are ever
// shown.

const RUNNING_CASE_RE = /RUNNING test_case:([0-9a-fA-F-]{8,})/;
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
    if (match) found = match[1]!; // last marker across the seq-ordered stream wins
  }
  return found;
}

function extractCaseVerdicts(messages: TaskMessagePayload[]): Record<string, "pass" | "fail"> {
  const out: Record<string, "pass" | "fail"> = {};
  for (const m of messages) {
    if (!m.content) continue;
    for (const match of m.content.matchAll(CASE_RESULT_RE)) {
      out[match[1]!] = match[2]!.toLowerCase() as "pass" | "fail";
    }
  }
  return out;
}

// The QA-run signal — shared with the QA lens so the live browser bay knows
// when to auto-open (docs: signal-driven live bay). One source of truth: both
// this component's marker-watching and the lens's auto-open decision read the
// SAME filtered task list, so they can never disagree about "is QA running".
//
// QA agents only — a knowledge-capture / dev task running on the same issue
// must not count as "QA is running". Leader + agent members of any squad
// named like "qa"; empty set (no QA squad) → no filter (show all).
export function useQaRunningTasks(issueId: string): AgentTask[] {
  const wsId = useWorkspaceId();
  const { data: snapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));

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
          /* best-effort */
        }
      }
      return ids;
    },
    staleTime: 300_000,
  });

  return useMemo(
    () =>
      snapshot.filter(
        (task) =>
          task.issue_id === issueId &&
          task.status === "running" &&
          (!qaAgentIds || qaAgentIds.size === 0 || qaAgentIds.has(task.agent_id)),
      ),
    [snapshot, issueId, qaAgentIds],
  );
}

export function QALiveProgress({ issueId }: { issueId: string }) {
  const runningTasks = useQaRunningTasks(issueId);

  return (
    <>
      {/* Headless: parse the run markers into the caches the Test-cases panel
          reads. Renders nothing — no tool-call log. */}
      {runningTasks.map((task) => (
        <QAMarkerWatcher key={task.id} task={task} issueId={issueId} />
      ))}
      <QARunStatusStrip issueId={issueId} running={runningTasks.length > 0} />
    </>
  );
}

// Headless per-task watcher: subscribes to the task's live message stream and
// writes the running-case + verdict caches. Returns null (no UI).
function QAMarkerWatcher({ task, issueId }: { task: AgentTask; issueId: string }) {
  const qc = useQueryClient();
  const { data: messages = [] } = useQuery(taskMessagesOptions(task.id));

  const runningCaseId = useMemo(() => extractRunningCaseId(messages), [messages]);
  const caseVerdicts = useMemo(() => extractCaseVerdicts(messages), [messages]);

  useEffect(() => {
    qc.setQueryData(runningCaseQueryKey(issueId), runningCaseId);
    return () => {
      qc.setQueryData(runningCaseQueryKey(issueId), (old: string | null | undefined) =>
        old === runningCaseId ? null : old,
      );
    };
  }, [qc, issueId, runningCaseId]);

  useEffect(() => {
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

  return null;
}

// The slim, clean run-status strip: "▸ Running <case title> · ✓a ✗b / N" while
// a QA run is active. No tool calls — just which case is under test right now
// and the running tally. Renders nothing when idle.
function QARunStatusStrip({ issueId, running }: { issueId: string; running: boolean }) {
  const { t } = useT("issues");
  const runningCaseId = useRunningTestCaseId(issueId);
  const verdicts = useLiveCaseVerdicts(issueId);
  const { data } = useQuery(testCasesOptions(issueId));
  const cases = data?.test_cases ?? [];

  if (!running) return null;

  const runningTitle = cases.find((c) => c.id === runningCaseId)?.title ?? "";
  const passed = Object.values(verdicts).filter((v) => v === "pass").length;
  const failed = Object.values(verdicts).filter((v) => v === "fail").length;
  const total = cases.length;

  return (
    <div
      className="flex items-center gap-2 rounded-md border bg-card px-2.5 py-1.5 text-[12px]"
      aria-live="polite"
    >
      <FlaskConical className="size-3.5 shrink-0 text-info" />
      <Loader2 className="size-3 shrink-0 animate-spin text-info" aria-hidden />
      <span className="min-w-0 flex-1 truncate">
        {runningTitle ? (
          <>
            <span className="text-muted-foreground">{t(($) => $.test_cases.runs_now)}: </span>
            <span className="font-medium">{runningTitle}</span>
          </>
        ) : (
          <span className="text-muted-foreground">{t(($) => $.qa_review.running_qa)}</span>
        )}
      </span>
      {(passed > 0 || failed > 0) && (
        <span className="flex shrink-0 items-center gap-2 text-[11px]">
          <span className="flex items-center gap-0.5 text-emerald-500">
            <CheckCircle2 className="size-3" /> {passed}
          </span>
          {failed > 0 && (
            <span className="flex items-center gap-0.5 text-destructive">
              <XCircle className="size-3" /> {failed}
            </span>
          )}
          {total > 0 && <span className="text-muted-foreground">/ {total}</span>}
        </span>
      )}
    </div>
  );
}
