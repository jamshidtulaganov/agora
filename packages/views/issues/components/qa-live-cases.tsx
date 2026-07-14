"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  CheckCircle2,
  XCircle,
  MinusCircle,
  AlertTriangle,
  Circle,
  Loader2,
} from "lucide-react";
import { testCasesOptions } from "@agora/core/issues/queries";
import type { TestCase } from "@agora/core/types/test-case";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { useRunningTestCaseId, useLiveCaseVerdicts } from "../../qa/components/qa-live-progress";

// ─────────────────────────────────────────────────────────────────────────────
// Live QA test-case panel. During the QA stage the generic live process only
// shows tool-level activity ("running the tests") — it never surfaces WHICH
// cases the QA agent is exercising or how each one landed. This panel closes
// that gap: it lists the issue's actual test cases and their live verdicts,
// keyed off the same testCasesOptions query the QA section reads. The
// `test_cases:changed` WS event (fired by CaptureTestRuns when the agent
// records its run verdicts) invalidates that query, so the rows tick from
// pending → pass/fail without a reload. No new endpoint, no new event.
//
// Self-hides when the issue has no test cases, so it never shows an empty box.
// ─────────────────────────────────────────────────────────────────────────────

type CaseState = "pass" | "fail" | "skip" | "blocked" | "pending";

function caseState(c: TestCase): CaseState {
  const s = (c.latest_run?.status ?? "").toLowerCase();
  if (s === "pass" || s === "fail" || s === "skip" || s === "blocked") return s;
  return "pending";
}

// Stable ordering: failures first (a human must see them), then the still-running
// pending set, then the settled pass/skip — without re-sorting on every tick
// (keep authoring order WITHIN a bucket so rows don't jump as verdicts land).
const STATE_RANK: Record<CaseState, number> = {
  fail: 0,
  blocked: 1,
  pending: 2,
  pass: 3,
  skip: 4,
};

function StateIcon({ state, running }: { state: CaseState; running: boolean }) {
  switch (state) {
    case "pass":
      return <CheckCircle2 aria-hidden className="mt-px size-3.5 shrink-0 text-emerald-600 dark:text-emerald-400" />;
    case "fail":
      return <XCircle aria-hidden className="mt-px size-3.5 shrink-0 text-destructive" />;
    case "skip":
      return <MinusCircle aria-hidden className="mt-px size-3.5 shrink-0 text-muted-foreground/60" />;
    case "blocked":
      return <AlertTriangle aria-hidden className="mt-px size-3.5 shrink-0 text-amber-500" />;
    default:
      // Pending: a spinner while QA is actively running, a dim circle otherwise.
      return running ? (
        <Loader2 aria-hidden className="mt-px size-3.5 shrink-0 animate-spin text-info" />
      ) : (
        <Circle aria-hidden className="mt-px size-3.5 shrink-0 text-muted-foreground/40" />
      );
  }
}

// Compact nested variant — test-case runs as CHILDREN of a plan step (the
// to-do progress → child shape). No rollup header, smaller rows, indented by
// the parent. Renders nothing when the issue has no cases.
//
// QA runs cases ONE AT A TIME — the agent emits a `RUNNING test_case:<id>`
// marker per case (useRunningTestCaseId) and a live `QA_RESULT` verdict
// (useLiveCaseVerdicts). Only the marker-named case gets the spinner; the
// rest of the pending set stays a dim circle. Spinning every pending row
// (the first version of this list) read as "everything runs at once", which
// is not what happens.
export function QALiveCaseChildren({ issueId }: { issueId: string }) {
  const { data } = useQuery(testCasesOptions(issueId));
  const runningCaseId = useRunningTestCaseId(issueId);
  const liveVerdicts = useLiveCaseVerdicts(issueId);
  const cases = data?.test_cases ?? [];
  const ordered = useMemo(
    () =>
      cases
        .map((c, i) => {
          // Live stream verdict wins over the (not-yet-persisted) latest_run.
          const live = liveVerdicts[c.id];
          const state: CaseState = live ?? caseState(c);
          return { c, i, state };
        })
        .sort((a, b) => STATE_RANK[a.state] - STATE_RANK[b.state] || a.i - b.i),
    [cases, liveVerdicts],
  );
  if (cases.length === 0) return null;
  return (
    <ul className="mt-1 flex flex-col gap-0.5 border-l border-border/60 pl-3">
      {ordered.map(({ c, state }) => {
        const isRunningNow = c.id === runningCaseId;
        return (
          <li key={c.id} className="flex items-start gap-1.5 text-[11px]" aria-current={isRunningNow || undefined}>
            <StateIcon state={state} running={isRunningNow} />
            <span
              className={cn(
                "min-w-0 flex-1 truncate",
                isRunningNow && "font-medium text-info",
                !isRunningNow && state === "fail" && "font-medium text-destructive",
                !isRunningNow && state === "pass" && "text-muted-foreground/70 line-through",
                !isRunningNow && state === "pending" && "text-muted-foreground",
                (state === "skip" || state === "blocked") && "text-muted-foreground",
              )}
              title={c.latest_run?.output?.trim() || c.title}
            >
              {c.title}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

export function QALiveCases({ issueId, status }: { issueId: string; status: string }) {
  const { t } = useT("issues");
  const { data } = useQuery(testCasesOptions(issueId));
  const cases = data?.test_cases ?? [];

  const ordered = useMemo(
    () =>
      cases
        .map((c, i) => ({ c, i, state: caseState(c) }))
        .sort((a, b) => STATE_RANK[a.state] - STATE_RANK[b.state] || a.i - b.i),
    [cases],
  );

  // Nothing authored yet → nothing to show (the QA evidence section below still
  // renders its own "run QA" affordance).
  if (cases.length === 0) return null;

  const running = status === "in_review";
  const passed = ordered.filter((o) => o.state === "pass").length;
  const failed = ordered.filter((o) => o.state === "fail").length;

  return (
    <div className="mb-2">
      <div className="mb-1 flex items-center gap-1.5 px-1 text-xs font-medium">
        <span>{t(($) => $.qa_cases.title)}</span>
        {running && failed === 0 && (
          <Loader2 aria-hidden className="size-3 shrink-0 animate-spin text-info" />
        )}
        <span className="ml-auto font-mono text-[10px] tabular-nums text-muted-foreground">
          {t(($) => $.qa_cases.progress, { passed, total: cases.length })}
          {failed > 0 && (
            <span className="ml-1 text-destructive">
              {t(($) => $.qa_cases.failed, { count: failed })}
            </span>
          )}
        </span>
      </div>

      <ul className="flex flex-col gap-0.5 pl-1">
        {ordered.map(({ c, state }) => {
          const output = c.latest_run?.output?.trim();
          return (
            <li key={c.id} className="flex items-start gap-1.5 text-[11px]" aria-current={state === "pending" && running ? true : undefined}>
              <StateIcon state={state} running={running} />
              <div className="flex min-w-0 flex-1 flex-col">
                <div className="flex min-w-0 items-center gap-1.5">
                  <span
                    className={cn(
                      "min-w-0 truncate",
                      state === "fail" && "font-medium text-foreground",
                      state === "pass" && "text-muted-foreground",
                      state === "pending" && "text-foreground/90",
                      (state === "skip" || state === "blocked") && "text-muted-foreground",
                    )}
                    title={c.title}
                  >
                    {c.title}
                  </span>
                  {c.category === "negative" && (
                    <span className="shrink-0 rounded bg-muted px-1 text-[9px] uppercase tracking-wide text-muted-foreground">
                      {t(($) => $.qa_cases.negative)}
                    </span>
                  )}
                  {c.flaky && (
                    <span className="shrink-0 rounded bg-amber-500/15 px-1 text-[9px] uppercase tracking-wide text-amber-600 dark:text-amber-400">
                      {t(($) => $.qa_cases.flaky)}
                    </span>
                  )}
                </div>
                {state === "fail" && output && (
                  <span className="min-w-0 truncate text-[10px] text-destructive/80" title={output}>
                    {output}
                  </span>
                )}
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
