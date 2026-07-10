"use client";

import { useState } from "react";
import { Check, X, CircleSlash, Loader2 } from "lucide-react";
import {
  deriveStepRunVerdict,
  serializeStepResults,
  type StepResult,
  type StepResultStatus,
} from "@agora/core/qa/step-run";
import type { ParsedStep } from "@agora/core/qa/steps";
import { Button } from "@agora/ui/components/ui/button";
import { Input } from "@agora/ui/components/ui/input";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";

// TestRail-style per-step manual run (QA lens phase 4): a QA engineer walks a
// case's structured steps by hand, marking each pass / fail / skip (with an
// actual-result note on a fail). Finishing derives the CASE verdict and hands
// the caller ONE serialized output blob — the row records it through the SAME
// human run path the one-click ✓/✗ uses (POST /api/test-cases/:id/runs), so
// no new machinery; the per-step breakdown rides in the run's output text
// (see @agora/core/qa/step-run).

export function StepRunChecklist({
  steps,
  busy,
  onFinish,
  onCancel,
}: {
  steps: ParsedStep[];
  busy: boolean;
  // verdict + serialized output for the ONE test_run row this walk records.
  onFinish: (verdict: "pass" | "fail" | "skip", output: string) => void;
  onCancel: () => void;
}) {
  const { t } = useT("issues");
  const [marks, setMarks] = useState<(StepResultStatus | null)[]>(() => steps.map(() => null));
  const [notes, setNotes] = useState<string[]>(() => steps.map(() => ""));

  const setMark = (i: number, status: StepResultStatus) =>
    setMarks((m) => m.map((v, idx) => (idx === i ? (v === status ? null : status) : v)));
  const setNote = (i: number, note: string) => setNotes((n) => n.map((v, idx) => (idx === i ? note : v)));

  const markedCount = marks.filter((m) => m !== null).length;
  // The step under the engineer's cursor conceptually: first unmarked one.
  const currentIndex = marks.findIndex((m) => m === null);
  const anyFail = marks.some((m) => m === "fail");
  // Finish when every step is marked — or early once something failed (a
  // fail already decides the case; remaining steps record as skipped).
  const canFinish = markedCount === steps.length || anyFail;

  const finish = () => {
    const results: StepResult[] = steps.map((_, i) => {
      const status = marks[i] ?? "skip";
      const note = notes[i]?.trim();
      return note ? { step: i + 1, status, note } : { step: i + 1, status };
    });
    onFinish(deriveStepRunVerdict(results), serializeStepResults(results));
  };

  return (
    <div className="mt-1.5 space-y-1.5 rounded-md border bg-muted/20 p-2">
      <p className="text-[11px] font-medium text-muted-foreground" aria-live="polite">
        {currentIndex === -1
          ? t(($) => $.test_cases.checklist_done)
          : t(($) => $.test_cases.checklist_header, { current: currentIndex + 1, total: steps.length })}
      </p>
      <ol className="list-none space-y-1">
        {steps.map((s, i) => (
          <li key={i} className={cn("rounded px-1.5 py-1", i === currentIndex && "bg-muted/50")}>
            <div className="flex items-start gap-1.5">
              <span className="mt-0.5 w-4 shrink-0 text-right text-[11px] text-muted-foreground" aria-hidden>
                {i + 1}.
              </span>
              <span className="min-w-0 flex-1 text-[12px]">
                {s.action}
                {s.expects && <span className="text-muted-foreground"> — {s.expects}</span>}
              </span>
              <span className="flex shrink-0 items-center gap-0.5">
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  onClick={() => setMark(i, "pass")}
                  className={cn(
                    "size-6 text-muted-foreground hover:bg-emerald-600/10",
                    marks[i] === "pass" && "bg-emerald-600/10 text-emerald-700 dark:text-emerald-300",
                  )}
                  title={t(($) => $.test_cases.run_pass)}
                >
                  <Check className="size-3.5" />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  onClick={() => setMark(i, "fail")}
                  className={cn(
                    "size-6 text-muted-foreground hover:bg-destructive/10",
                    marks[i] === "fail" && "bg-destructive/10 text-destructive",
                  )}
                  title={t(($) => $.test_cases.run_fail)}
                >
                  <X className="size-3.5" />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  onClick={() => setMark(i, "skip")}
                  className={cn(
                    "size-6 text-muted-foreground hover:bg-amber-500/10",
                    marks[i] === "skip" && "bg-amber-500/10 text-amber-600 dark:text-amber-400",
                  )}
                  title={t(($) => $.test_cases.step_skip)}
                >
                  <CircleSlash className="size-3.5" />
                </Button>
              </span>
            </div>
            {marks[i] === "fail" && (
              <Input
                value={notes[i] ?? ""}
                onChange={(e) => setNote(i, e.target.value)}
                placeholder={t(($) => $.test_cases.checklist_note_ph)}
                aria-label={t(($) => $.test_cases.checklist_note_ph)}
                className="mt-1 h-7 text-[12px]"
              />
            )}
          </li>
        ))}
      </ol>
      <div className="flex items-center justify-end gap-1.5">
        <Button type="button" variant="ghost" size="sm" className="h-7 text-[12px]" onClick={onCancel}>
          {t(($) => $.test_cases.cancel)}
        </Button>
        <Button
          type="button"
          size="sm"
          className="h-7 text-[12px]"
          disabled={!canFinish || busy}
          onClick={finish}
        >
          {busy ? <Loader2 className="size-3.5 animate-spin" /> : t(($) => $.test_cases.checklist_finish)}
        </Button>
      </div>
    </div>
  );
}

// Read-only breakdown of a recorded manual step run — parsed back out of the
// run's output fence by the caller. Distinct from an agent run's free-text
// evidence: each step renders with its own verdict icon + note, resolving the
// step's action text from the case's current steps when positions still line
// up (the case may have been edited since the run — fall back to the number).
export function StepResultList({ results, steps }: { results: StepResult[]; steps: ParsedStep[] }) {
  const { t } = useT("issues");
  return (
    <div className="space-y-0.5">
      <p className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground/70">
        {t(($) => $.test_cases.manual_run_label)}
      </p>
      <ol className="list-none space-y-0.5">
        {results.map((r) => (
          <li key={r.step} className="flex items-start gap-1.5 text-[12px]">
            <span className="mt-0.5 shrink-0" aria-hidden>
              {r.status === "pass" ? (
                <Check className="size-3 text-emerald-700 dark:text-emerald-300" />
              ) : r.status === "fail" ? (
                <X className="size-3 text-destructive" />
              ) : (
                <CircleSlash className="size-3 text-amber-600 dark:text-amber-400" />
              )}
            </span>
            <span className="min-w-0 flex-1">
              <span className="text-foreground/70">{r.step}.</span> {steps[r.step - 1]?.action ?? ""}
              {r.note && <span className="text-muted-foreground"> — {r.note}</span>}
            </span>
          </li>
        ))}
      </ol>
    </div>
  );
}
