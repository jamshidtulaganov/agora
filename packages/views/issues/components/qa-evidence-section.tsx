"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  Circle,
  FlaskConical,
  Loader2,
  MinusCircle,
  RefreshCw,
  ShieldQuestion,
  Terminal,
  XCircle,
} from "lucide-react";
import { toast } from "sonner";
import { api } from "@agora/core/api";
import { issueKeys, qaEvidenceOptions, testCasesOptions } from "@agora/core/issues/queries";
import type { TestCase } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { StructuredResult } from "./qa-result";
import { QADesignCompare } from "../../qa/components/qa-design-compare";

// Evidence-first QA section. Opening an in-review task shows its frozen run_qa
// verdict — the command table with new-failure attribution — read from ONE
// indexed qa_evidence row (api.getQAEvidence), not by re-parsing the timeline.
// This is the default "QA environment": a deterministic, attributable verdict
// that scales to many concurrent task-opens with zero per-task box. A live
// preview box (the rare interactive case) is a later, on-demand escape hatch.
//
// Gating: rendered by issue-detail for QA-relevant statuses; the component
// hides itself when there's no evidence AND the issue is not in_review, so it
// never shows an empty box on a backlog/in-progress task.

interface QAEvidenceSectionProps {
  issueId: string;
  status: string;
  allowRerun?: boolean;
}

type HumanCaseState = "pass" | "fail" | "blocked" | "skip" | "pending";

function humanCaseState(testCase: TestCase): HumanCaseState {
  const status = testCase.latest_run?.status?.toLowerCase();
  if (status === "pass" || status === "fail" || status === "blocked" || status === "skip") return status;
  return "pending";
}

const CASE_STATE_RANK: Record<HumanCaseState, number> = {
  fail: 0,
  blocked: 1,
  pending: 2,
  pass: 3,
  skip: 4,
};

function CaseStateIcon({ state }: { state: HumanCaseState }) {
  if (state === "pass") return <CheckCircle2 className="size-4 text-emerald-600 dark:text-emerald-400" aria-hidden />;
  if (state === "fail") return <XCircle className="size-4 text-destructive" aria-hidden />;
  if (state === "blocked") return <AlertTriangle className="size-4 text-amber-500" aria-hidden />;
  if (state === "skip") return <MinusCircle className="size-4 text-muted-foreground" aria-hidden />;
  return <Circle className="size-4 text-muted-foreground/50" aria-hidden />;
}

function TestCaseReport({ cases }: { cases: TestCase[] }) {
  const { t } = useT("issues");
  const ordered = [...cases].sort((a, b) => CASE_STATE_RANK[humanCaseState(a)] - CASE_STATE_RANK[humanCaseState(b)]);
  const failed = cases.filter((testCase) => humanCaseState(testCase) === "fail").length;
  const passed = cases.filter((testCase) => humanCaseState(testCase) === "pass").length;

  const stateLabel = (state: HumanCaseState) => {
    if (state === "pass") return t(($) => $.qa_evidence.case_passed);
    if (state === "fail") return t(($) => $.qa_evidence.case_failed);
    if (state === "blocked") return t(($) => $.qa_evidence.case_blocked);
    if (state === "skip") return t(($) => $.qa_evidence.case_skipped);
    return t(($) => $.qa_evidence.case_not_run);
  };

  return (
    <section className="space-y-2" aria-labelledby="qa-test-cases-title">
      <div className="flex flex-wrap items-center gap-2">
        <FlaskConical className="size-4 text-muted-foreground" aria-hidden />
        <h3 id="qa-test-cases-title" className="text-xs font-medium">
          {t(($) => $.qa_evidence.test_cases_title)}
        </h3>
        <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] tabular-nums text-muted-foreground">
          {t(($) => $.qa_evidence.test_cases_progress, { passed, total: cases.length })}
        </span>
        {failed > 0 && (
          <span className="text-[10px] font-medium text-destructive">
            {t(($) => $.qa_evidence.test_cases_failed, { count: failed })}
          </span>
        )}
      </div>
      <p className="text-[11px] text-muted-foreground">{t(($) => $.qa_evidence.test_cases_description)}</p>

      <ul className="overflow-hidden rounded-lg border border-border/60">
        {ordered.map((testCase) => {
          const state = humanCaseState(testCase);
          const observed = testCase.latest_run?.output?.trim();
          const needsAttention = state === "fail" || state === "blocked";
          return (
            <li key={testCase.id} className="border-t border-border/60 first:border-t-0">
              <details className="group" open={needsAttention}>
                <summary className="flex cursor-pointer list-none items-start gap-2.5 px-3 py-2.5 hover:bg-muted/25 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring">
                  <span className="mt-px shrink-0"><CaseStateIcon state={state} /></span>
                  <span className="min-w-0 flex-1 text-xs font-medium text-foreground/90">{testCase.title}</span>
                  <span className={cn(
                    "shrink-0 text-[10px]",
                    state === "fail" && "font-medium text-destructive",
                    state === "pass" && "text-emerald-600 dark:text-emerald-400",
                    state !== "fail" && state !== "pass" && "text-muted-foreground",
                  )}>
                    {stateLabel(state)}
                  </span>
                  <ChevronDown className="mt-px size-3.5 shrink-0 text-muted-foreground transition-transform group-open:rotate-180 motion-reduce:transition-none" aria-hidden />
                </summary>
                <div className="border-t border-border/50 bg-muted/10 px-4 py-3 pl-9">
                  {testCase.criterion_ref && (
                    <div className="mb-2 text-[10px] text-muted-foreground">
                      <span className="font-medium">{t(($) => $.qa_evidence.requirement)}:</span> {testCase.criterion_ref}
                    </div>
                  )}
                  <dl className="grid gap-2 sm:grid-cols-2">
                    <div className="rounded-md border bg-background p-2.5">
                      <dt className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                        {t(($) => $.qa_evidence.expected)}
                      </dt>
                      <dd className="mt-1 whitespace-pre-wrap text-[11px] text-foreground/90">
                        {testCase.expected.trim() || testCase.title}
                      </dd>
                    </div>
                    <div className={cn("rounded-md border bg-background p-2.5", needsAttention && "border-destructive/25")}>
                      <dt className={cn("text-[10px] font-medium uppercase tracking-wide text-muted-foreground", needsAttention && "text-destructive/80")}>
                        {t(($) => $.qa_evidence.observed)}
                      </dt>
                      <dd className="mt-1 whitespace-pre-wrap text-[11px] text-foreground/90">
                        {observed || (state === "pending"
                          ? t(($) => $.qa_evidence.observed_not_run)
                          : state === "pass"
                            ? t(($) => $.qa_evidence.observed_as_expected)
                            : t(($) => $.qa_evidence.observed_not_recorded))}
                      </dd>
                    </div>
                  </dl>
                  {(testCase.preconditions.trim() || testCase.steps.trim()) && (
                    <div className="mt-3 grid gap-2 text-[11px] text-muted-foreground sm:grid-cols-2">
                      {testCase.preconditions.trim() && (
                        <div>
                          <span className="font-medium text-foreground/75">{t(($) => $.qa_evidence.setup)}:</span>{" "}
                          <span className="whitespace-pre-wrap">{testCase.preconditions}</span>
                        </div>
                      )}
                      {testCase.steps.trim() && (
                        <div>
                          <span className="font-medium text-foreground/75">{t(($) => $.qa_evidence.how_checked)}:</span>{" "}
                          <span className="whitespace-pre-wrap">{testCase.steps}</span>
                        </div>
                      )}
                    </div>
                  )}
                </div>
              </details>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function summaryLooksTechnical(value: string): boolean {
  return /\/(?:private\/)?tmp\/|\b(?:docs|src|server|packages|apps)\/|\.m?js\b|\b(?:node|pnpm|npm|yarn|go test)\b|baseline|branch|exit\s+code/i.test(value);
}

export function QAEvidenceSection({ issueId, status, allowRerun = true }: QAEvidenceSectionProps) {
  const { t } = useT("issues");
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(true);
  const [rerunning, setRerunning] = useState(false);

  const { data: evidence, isLoading } = useQuery(qaEvidenceOptions(issueId));
  const { data: testCaseData } = useQuery(testCasesOptions(issueId));

  // Rail simplicity: render ONLY when a verdict actually exists. The old
  // in_review empty state (dashed "no verdict yet" box + hint + Re-run) was
  // noise — the plan card + stepper already show QA running; this box appears
  // the moment there is a verdict to show.
  if (!evidence) return null;
  void status;

  const verdict = evidence?.verdict ?? "";
  const verdictIcon =
    verdict === "pass" ? (
      <CheckCircle2 className="size-3.5 shrink-0 text-emerald-600 dark:text-emerald-400" />
    ) : verdict === "fail" ? (
      <XCircle className="size-3.5 shrink-0 text-destructive" />
    ) : (
      <ShieldQuestion className="size-3.5 shrink-0 text-muted-foreground" />
    );
  const verdictLabel =
    verdict === "pass"
      ? t(($) => $.qa_evidence.verdict_pass)
      : verdict === "fail"
        ? t(($) => $.qa_evidence.verdict_fail)
        : t(($) => $.qa_evidence.verdict_unknown);
  const cases = testCaseData?.test_cases ?? [];
  const rawSummary = evidence.summary?.trim() || evidence.result?.summary?.trim() || "";
  const humanSummary = rawSummary && !summaryLooksTechnical(rawSummary)
    ? rawSummary
    : verdict === "fail"
      ? t(($) => $.qa_evidence.failure_summary)
      : t(($) => $.qa_evidence.pass_summary);

  const rerun = async () => {
    if (rerunning) return;
    setRerunning(true);
    try {
      await api.sliceAction(issueId, { kind: "run_qa" });
      void queryClient.invalidateQueries({ queryKey: issueKeys.tasks(issueId) });
      void queryClient.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
      toast.success(t(($) => $.slice_actions.toast_fired));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.slice_actions.toast_failed));
    } finally {
      setRerunning(false);
    }
  };

  return (
    <section className={cn(
      "overflow-hidden rounded-xl border bg-card",
      verdict === "fail" && "border-destructive/25",
      verdict === "pass" && "border-emerald-500/20",
    )}>
      <button
        type="button"
        className={cn(
          "flex w-full items-start gap-3 px-4 py-3.5 text-left transition-colors hover:bg-muted/25 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
          verdict === "fail" && "bg-destructive/[0.025]",
          verdict === "pass" && "bg-emerald-500/[0.025]",
        )}
        onClick={() => setOpen(!open)}
        aria-expanded={open}
      >
        <span className="mt-0.5">{verdictIcon}</span>
        <span className="min-w-0 flex-1">
          <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="text-sm font-semibold">{t(($) => $.qa_evidence.section)}</span>
            <span className={cn(
              "text-xs font-medium",
              verdict === "fail" && "text-destructive",
              verdict === "pass" && "text-emerald-600 dark:text-emerald-400",
              verdict !== "fail" && verdict !== "pass" && "text-muted-foreground",
            )}>{verdictLabel}</span>
          </span>
          <span className="mt-0.5 block text-[11px] font-normal text-muted-foreground">{humanSummary}</span>
        </span>
        <span className="flex shrink-0 items-center gap-2">
          {evidence.captured_at && (
            <time className="hidden text-[10px] font-normal text-muted-foreground sm:inline" dateTime={evidence.captured_at}>
              {new Date(evidence.captured_at).toLocaleString()}
            </time>
          )}
          <ChevronDown className={cn("size-4 text-muted-foreground transition-transform motion-reduce:transition-none", open && "rotate-180")} aria-hidden />
        </span>
      </button>

      {open && (
        <div className="border-t">
          {isLoading ? (
            <div className="flex items-center gap-2 px-4 py-6 text-[11px] text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" />
            </div>
          ) : evidence ? (
            <div className="space-y-5 px-4 py-4">
              {cases.length > 0 ? (
                <TestCaseReport cases={cases} />
              ) : (
                <div className="flex items-start gap-2 rounded-lg border border-amber-500/20 bg-amber-500/[0.04] px-3 py-2.5 text-[11px] text-muted-foreground">
                  <ShieldQuestion className="mt-px size-4 shrink-0 text-amber-500" aria-hidden />
                  <div>
                    <p className="font-medium text-foreground/85">{t(($) => $.qa_evidence.no_named_cases)}</p>
                    <p className="mt-0.5">{t(($) => $.qa_evidence.no_named_cases_hint)}</p>
                  </div>
                </div>
              )}
              {evidence.result && evidence.result.commands.length > 0 && (
                cases.length > 0 ? (
                  <details className="group overflow-hidden rounded-lg border border-border/60 bg-muted/10">
                    <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2.5 text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring">
                      <Terminal className="size-3.5" aria-hidden />
                      <span>{t(($) => $.qa_evidence.technical_details)}</span>
                      <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] tabular-nums">
                        {t(($) => $.qa_evidence.checks_count, { count: evidence.result.commands.length })}
                      </span>
                      <ChevronDown className="ml-auto size-3.5 transition-transform group-open:rotate-180 motion-reduce:transition-none" aria-hidden />
                    </summary>
                    <div className="border-t border-border/60">
                      <StructuredResult result={evidence.result} />
                    </div>
                  </details>
                ) : (
                  <StructuredResult result={evidence.result} />
                )
              )}
              {evidence.result?.design && (
                <div className="mt-2">
                  <QADesignCompare design={evidence.result.design} />
                </div>
              )}
            </div>
          ) : (
            <div className="m-4 rounded-md border border-dashed border-border bg-muted/20 px-3 py-4 text-center">
              <p className="text-[11px] text-muted-foreground">{t(($) => $.qa_evidence.empty)}</p>
              <p className="mt-0.5 text-[10px] text-muted-foreground/70">{t(($) => $.qa_evidence.empty_hint)}</p>
            </div>
          )}

          {allowRerun && <div className="border-t bg-muted/10 px-4 py-3">
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-8 w-full text-xs sm:w-auto"
              disabled={rerunning}
              onClick={rerun}
            >
              {rerunning ? (
                <>
                  <Loader2 className="size-3.5 animate-spin" />
                  {t(($) => $.qa_evidence.rerunning)}
                </>
              ) : (
                <>
                  <RefreshCw className="size-3.5" />
                  {t(($) => $.qa_evidence.rerun)}
                </>
              )}
            </Button>
          </div>}
        </div>
      )}
    </section>
  );
}
