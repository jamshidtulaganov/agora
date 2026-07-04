"use client";

import { Fragment } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  CheckCircle2,
  XCircle,
  Clock,
  ShieldCheck,
  FlaskConical,
  ChevronDown,
  ChevronRight,
  Terminal,
  Loader2,
  Play,
} from "lucide-react";
import type { TestRunState } from "./editor-preview-pane";
import { useState } from "react";
import { api } from "@agora/core/api";
import { issueTimelineOptions } from "@agora/core/issues/queries";
import { cn } from "@agora/ui/lib/utils";
import type { TimelineEntry, QACommand, QAResult } from "@agora/core/types";

// Tests panel for the editor right sidebar. Shows:
//  1. Merge-readiness gate chips (ci / qa) — live, polled every 15s.
//  2. QA verdict comments from the issue timeline — any comment that mentions
//     exit codes or sets a qa:pass/qa:fail label is surfaced here with an
//     expandable raw output section so the human can see why it failed.
//
// Design intent: the agent is the one that RUNS the tests and posts the verdict.
// This panel surfaces that verdict in a structured way — no new backend needed,
// we parse the existing timeline comments.

// ─────────────────────────────────────────────────────────────────────────────
// Gate chips from MergeReadiness
// ─────────────────────────────────────────────────────────────────────────────

function GateChip({ name, status }: { name: string; status: string }) {
  const icon =
    status === "pass" ? (
      <CheckCircle2 className="h-3.5 w-3.5 shrink-0" />
    ) : status === "fail" ? (
      <XCircle className="h-3.5 w-3.5 shrink-0" />
    ) : (
      <Clock className="h-3.5 w-3.5 shrink-0 animate-pulse" />
    );

  return (
    <div
      className={cn(
        "flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-xs font-medium",
        status === "pass" &&
          "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
        status === "fail" &&
          "border-destructive/30 bg-destructive/10 text-destructive",
        status === "pending" &&
          "border-border bg-muted/40 text-muted-foreground",
      )}
    >
      {icon}
      <span className="uppercase tracking-wide">{name}</span>
    </div>
  );
}

function GatesSection({ issueId }: { issueId: string }) {
  const { data } = useQuery({
    queryKey: ["merge-readiness", issueId],
    queryFn: () => api.mergeReadiness(issueId),
    enabled: !!issueId,
    refetchInterval: 15000,
  });

  if (!data) {
    return (
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <Clock className="h-3.5 w-3.5 animate-pulse" />
        Loading gate status…
      </div>
    );
  }

  const overallIcon =
    data.ready ? (
      <CheckCircle2 className="h-4 w-4 text-emerald-500" />
    ) : (
      <XCircle className="h-4 w-4 text-destructive" />
    );

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 text-xs">
        {overallIcon}
        <span className="font-medium">
          {data.ready ? "Ready to merge" : "Not ready"}
        </span>
        <span className="ml-auto rounded bg-muted/60 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
          {data.tier}
        </span>
      </div>
      <div className="flex flex-wrap gap-1.5">
        {data.gates.map((g) => (
          <GateChip key={g.name} name={g.name} status={g.status} />
        ))}
        {data.gates.length === 0 && (
          <p className="text-[11px] text-muted-foreground">
            No gates yet — run QA to get a verdict.
          </p>
        )}
      </div>
      {data.blocked && data.blocked.length > 0 && (
        <ul className="space-y-0.5 pl-1">
          {data.blocked.map((b) => (
            <li
              key={b}
              className="flex items-center gap-1.5 text-[11px] text-destructive"
            >
              <span className="h-1 w-1 shrink-0 rounded-full bg-current" />
              {b}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// QA verdict parsing from timeline comments
// ─────────────────────────────────────────────────────────────────────────────

interface QAVerdict {
  id: string;
  verdict: "pass" | "fail" | "unknown";
  summary: string;
  raw: string;
  createdAt?: string;
  // Present when the comment carried a valid ```qa-result block — drives the
  // structured command table. Null for older/free-form verdicts.
  result?: QAResult | null;
}

// Heuristics: a QA verdict comment contains exit codes or qa label markers.
// The agent posts these via the run_qa / run_ci slice action recipe.
function parseQAVerdict(content: string): "pass" | "fail" | "unknown" {
  const lower = content.toLowerCase();
  if (lower.includes("qa:pass") || lower.includes("all checks passed")) {
    return "pass";
  }
  if (
    lower.includes("qa:fail") ||
    lower.includes("exit code 1") ||
    lower.includes("exit code: 1") ||
    lower.includes("failed") ||
    lower.includes("ci:fail")
  ) {
    return "fail";
  }
  return "unknown";
}

// Extract first line or short summary from comment content.
function summarize(content: string): string {
  const first = content.split("\n").find((l) => l.trim().length > 0) ?? "";
  return first.length > 120 ? first.slice(0, 117) + "…" : first;
}

// QA verdict comments: comments that mention exit codes, QA labels, or test
// results. We look for specific markers the run_qa recipe instructs the agent
// to include in its verdict comment.
function isQAComment(content: string): boolean {
  const lower = content.toLowerCase();
  return (
    lower.includes("qa:pass") ||
    lower.includes("qa:fail") ||
    lower.includes("ci:pass") ||
    lower.includes("ci:fail") ||
    lower.includes("exit code") ||
    lower.includes("pnpm test") ||
    lower.includes("go test") ||
    lower.includes("playwright") ||
    lower.includes("smoke") ||
    (lower.includes("all checks") &&
      (lower.includes("passed") || lower.includes("failed")))
  );
}

// QACommand / QAResult are the structured result the run_qa recipe appends as a
// fenced ```qa-result JSON block (now shared from @agora/core/types so the QA
// evidence section renders the SAME shape). Parsed deterministically when present
// (command table + baseline diff); falls back to the keyword heuristic otherwise.

// Extract + validate the ```qa-result block. The content is agent-authored, so
// every field is treated as possibly missing or wrong (parse, don't trust):
// returns null on any shape mismatch and the panel falls back to the raw view.
export function parseQAResultBlock(content: string): QAResult | null {
  const m = content.match(/```qa-result\s*\n([\s\S]*?)```/);
  if (!m) return null;
  let obj: unknown;
  try {
    obj = JSON.parse(m[1]!.trim());
  } catch {
    return null;
  }
  if (!obj || typeof obj !== "object") return null;
  const o = obj as Record<string, unknown>;
  const verdict =
    o.verdict === "pass" || o.verdict === "fail" ? o.verdict : null;
  if (!verdict) return null;
  const commands: QACommand[] = (Array.isArray(o.commands) ? o.commands : [])
    .filter((c): c is Record<string, unknown> => !!c && typeof c === "object")
    .map((c): QACommand => ({
      cmd: typeof c.cmd === "string" ? c.cmd : "",
      baseline_exit: typeof c.baseline_exit === "number" ? c.baseline_exit : null,
      branch_exit: typeof c.branch_exit === "number" ? c.branch_exit : null,
      kind:
        c.kind === "new_failure"
          ? "new_failure"
          : c.kind === "pre_existing"
            ? "pre_existing"
            : "pass",
      error: typeof c.error === "string" ? c.error : "",
    }))
    .filter((c) => c.cmd.length > 0);
  const screenshots = (Array.isArray(o.screenshots) ? o.screenshots : []).filter(
    (s): s is string => typeof s === "string" && s.length > 0,
  );
  return {
    verdict,
    summary: typeof o.summary === "string" ? o.summary : "",
    commands,
    screenshots,
  };
}

const CMD_KIND_STYLE: Record<QACommand["kind"], { label: string; cls: string }> =
  {
    pass: { label: "pass", cls: "text-emerald-600 dark:text-emerald-400" },
    new_failure: { label: "new fail", cls: "text-destructive font-medium" },
    pre_existing: { label: "pre-existing", cls: "text-muted-foreground" },
  };

// Structured command table — cmd, baseline exit, branch exit, and the
// baseline-diff classification (pre-existing failures are visually de-emphasized
// so a NEW failure stands out as the thing that actually blocks the gate).
// Exported so the top-level QA evidence section renders the identical table.
export function StructuredResult({ result }: { result: QAResult }) {
  return (
    <div className="space-y-2 px-3 pb-2.5 pt-2">
      <div className="overflow-hidden rounded border border-border/60">
        <table className="w-full text-[10.5px]">
          <thead>
            <tr className="bg-muted/40 text-muted-foreground">
              <th className="px-2 py-1 text-left font-medium">command</th>
              <th className="px-1.5 py-1 text-center font-medium" title="baseline (merge-base) exit code">base</th>
              <th className="px-1.5 py-1 text-center font-medium" title="branch exit code">branch</th>
              <th className="px-2 py-1 text-right font-medium">result</th>
            </tr>
          </thead>
          <tbody className="font-mono">
            {result.commands.map((c, i) => {
              // Fall back for an unknown command kind: QAResultSchema keeps
              // kind as z.string(), so a newer server value must not deref undefined.
              const style = CMD_KIND_STYLE[c.kind] ?? CMD_KIND_STYLE.pre_existing;
              return (
                <Fragment key={i}>
                  <tr className="border-t border-border/40">
                    <td className="max-w-[180px] truncate px-2 py-1 text-foreground/80" title={c.cmd}>
                      {c.cmd}
                    </td>
                    <td className="px-1.5 py-1 text-center text-muted-foreground">
                      {c.baseline_exit === null ? "—" : c.baseline_exit}
                    </td>
                    <td className="px-1.5 py-1 text-center text-foreground/70">
                      {c.branch_exit === null ? "—" : c.branch_exit}
                    </td>
                    <td className={cn("px-2 py-1 text-right font-sans", style.cls)}>
                      {style.label}
                    </td>
                  </tr>
                  {/* WHY it failed — the agent's short reason, Jest-style: a
                      failing line is useless without the assertion/stderr that
                      explains it. Only rendered when the agent reported one. */}
                  {c.kind === "new_failure" && c.error && (
                    <tr className="border-t border-border/20 bg-destructive/5">
                      <td colSpan={4} className="px-2 py-1 font-sans text-[10px] text-destructive/90">
                        <pre className="whitespace-pre-wrap break-words">{c.error}</pre>
                      </td>
                    </tr>
                  )}
                </Fragment>
              );
            })}
          </tbody>
        </table>
      </div>
      {result.screenshots.length > 0 && (
        <div className="space-y-1">
          <div className="text-[10px] text-muted-foreground">Screenshots</div>
          <ul className="space-y-0.5">
            {result.screenshots.map((s, i) => (
              <li key={i} className="truncate font-mono text-[10px] text-foreground/60" title={s}>
                {s}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

function VerdictCard({ verdict }: { verdict: QAVerdict }) {
  const [expanded, setExpanded] = useState(false);

  const icon =
    verdict.verdict === "pass" ? (
      <CheckCircle2 className="h-4 w-4 shrink-0 text-emerald-500" />
    ) : verdict.verdict === "fail" ? (
      <XCircle className="h-4 w-4 shrink-0 text-destructive" />
    ) : (
      <Clock className="h-4 w-4 shrink-0 text-muted-foreground" />
    );

  return (
    <div
      className={cn(
        "rounded-md border text-xs",
        verdict.verdict === "pass" && "border-emerald-500/25 bg-emerald-500/5",
        verdict.verdict === "fail" && "border-destructive/25 bg-destructive/5",
        verdict.verdict === "unknown" && "border-border bg-muted/20",
      )}
    >
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="flex w-full items-start gap-2 px-3 py-2.5 text-left"
      >
        {icon}
        <span className="min-w-0 flex-1 break-words text-[11px] leading-snug text-foreground/80">
          {verdict.summary}
        </span>
        {expanded ? (
          <ChevronDown className="mt-0.5 h-3 w-3 shrink-0 text-muted-foreground" />
        ) : (
          <ChevronRight className="mt-0.5 h-3 w-3 shrink-0 text-muted-foreground" />
        )}
      </button>

      {expanded && (
        <div className="border-t border-border/60">
          {verdict.result && verdict.result.commands.length > 0 && (
            <StructuredResult result={verdict.result} />
          )}
          <div className="px-3 pb-2.5 pt-2">
            <div className="flex items-center gap-1.5 pb-1.5 text-[10px] text-muted-foreground">
              <Terminal className="h-3 w-3" />
              Full output
            </div>
            <pre className="max-h-64 overflow-y-auto whitespace-pre-wrap break-words rounded bg-muted/40 p-2 font-mono text-[10.5px] leading-relaxed text-foreground/70">
              {verdict.raw}
            </pre>
          </div>
        </div>
      )}
    </div>
  );
}

function VerdictSection({ issueId }: { issueId: string }) {
  const { data: timeline = [] } = useQuery(issueTimelineOptions(issueId));

  const verdicts: QAVerdict[] = (timeline as TimelineEntry[])
    .filter((e) => e.type === "comment" && isQAComment(e.content ?? ""))
    .map((e) => {
      const content = e.content ?? "";
      const result = parseQAResultBlock(content);
      return {
        id: e.id,
        // The structured block's verdict is authoritative when present; fall
        // back to the keyword heuristic for free-form comments.
        verdict: result ? result.verdict : parseQAVerdict(content),
        summary: result?.summary || summarize(content),
        raw: content,
        createdAt: e.created_at,
        result,
      };
    })
    .reverse(); // newest first

  if (verdicts.length === 0) {
    return (
      <div className="rounded-md border border-dashed border-border bg-muted/20 px-3 py-4 text-center">
        <FlaskConical className="mx-auto mb-1.5 h-5 w-5 text-muted-foreground/50" />
        <p className="text-[11px] text-muted-foreground">
          No test runs yet.
          <br />
          Click{" "}
          <span className="font-medium text-foreground/70">Run QA</span> above
          to trigger the gate.
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-1.5">
      {verdicts.map((v) => (
        <VerdictCard key={v.id} verdict={v} />
      ))}
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Live test run section — shows results from the Preview pane's daemon test run
// ─────────────────────────────────────────────────────────────────────────────

function LiveTestSection({
  testRunState,
  onRunTests,
}: {
  testRunState: TestRunState;
  onRunTests?: () => void;
}) {
  const [showRaw, setShowRaw] = useState(false);
  const { testState, testOut, testPassed, testCmd, parsedTests } = testRunState;

  if (testState === "idle") {
    return (
      <div className="rounded-md border border-dashed border-border bg-muted/20 px-3 py-4 text-center">
        <Play className="mx-auto mb-1.5 h-4 w-4 text-muted-foreground/50" />
        <p className="text-[11px] text-muted-foreground">
          No test run yet.
          {onRunTests && (
            <>
              {" "}
              <button
                type="button"
                onClick={onRunTests}
                className="font-medium text-foreground/70 underline-offset-2 hover:underline"
              >
                Run tests
              </button>{" "}
              from the Preview pane.
            </>
          )}
        </p>
      </div>
    );
  }

  if (testState === "running") {
    return (
      <div className="flex items-center gap-2 rounded-md border border-border bg-muted/20 px-3 py-3 text-xs text-muted-foreground">
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
        Running tests…
        {testCmd && (
          <span className="ml-auto font-mono text-[10px]">{testCmd}</span>
        )}
      </div>
    );
  }

  // done
  const overall = testPassed
    ? "pass"
    : testPassed === false
      ? "fail"
      : "unknown";

  return (
    <div
      className={cn(
        "rounded-md border text-xs",
        overall === "pass" && "border-emerald-500/25 bg-emerald-500/5",
        overall === "fail" && "border-destructive/25 bg-destructive/5",
        overall === "unknown" && "border-border bg-muted/20",
      )}
    >
      <div className="flex items-center gap-2 px-3 py-2">
        {overall === "pass" ? (
          <CheckCircle2 className="h-4 w-4 shrink-0 text-emerald-500" />
        ) : overall === "fail" ? (
          <XCircle className="h-4 w-4 shrink-0 text-destructive" />
        ) : (
          <Clock className="h-4 w-4 shrink-0 text-muted-foreground" />
        )}
        <span className="flex items-center gap-2 text-[11px]">
          <span className="rounded bg-emerald-500/15 px-1.5 py-0.5 font-medium text-emerald-600 dark:text-emerald-400">
            ✓ {parsedTests.passedCount} passed
          </span>
          {parsedTests.failedCount > 0 && (
            <span className="rounded bg-destructive/15 px-1.5 py-0.5 font-medium text-destructive">
              ✗ {parsedTests.failedCount} failed
            </span>
          )}
        </span>
        {testCmd && (
          <span className="ml-auto font-mono text-[10px] text-muted-foreground">
            {testCmd}
          </span>
        )}
        {testOut && (
          <button
            type="button"
            onClick={() => setShowRaw((v) => !v)}
            className="text-[10px] text-muted-foreground underline-offset-2 hover:underline"
          >
            {showRaw ? "cases" : "raw"}
          </button>
        )}
      </div>

      {showRaw && testOut ? (
        <pre
          className={cn(
            "max-h-48 overflow-auto border-t border-border/60 bg-black/90 p-2 font-mono text-[10px] leading-tight",
            testPassed === false ? "text-red-300" : "text-emerald-200",
          )}
        >
          {testOut}
        </pre>
      ) : (
        parsedTests.failed.length > 0 && (
          <ul className="divide-y divide-border/60 border-t border-border/60">
            {parsedTests.failed.map((t, i) => (
              <li
                key={`${t.file}-${i}`}
                className="flex items-start gap-1.5 px-3 py-1.5 text-[10px]"
              >
                <span className="mt-px text-destructive">✗</span>
                <span className="min-w-0">
                  <span className="text-foreground">{t.name}</span>
                  <span className="ml-1 font-mono text-muted-foreground/60">
                    {t.file.replace(/^.*\/tests?\//, "")}
                  </span>
                </span>
              </li>
            ))}
          </ul>
        )
      )}
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Main panel
// ─────────────────────────────────────────────────────────────────────────────

export function EditorTestsPanel({
  issueId,
  testRunState,
  onRunTests,
}: {
  issueId: string;
  testRunState?: TestRunState;
  onRunTests?: () => void;
}) {
  const defaultTestState: TestRunState = {
    testState: "idle",
    testOut: "",
    testPassed: null,
    testCmd: "",
    parsedTests: { failed: [], failedCount: 0, passedCount: 0 },
  };

  return (
    <div className="flex flex-col gap-4 p-3">
      <section className="space-y-2">
        <div className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
          <Play className="h-3.5 w-3.5" />
          Test Run
        </div>
        <LiveTestSection
          testRunState={testRunState ?? defaultTestState}
          onRunTests={onRunTests}
        />
      </section>

      <div className="h-px bg-border" />

      <section className="space-y-2">
        <div className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
          <ShieldCheck className="h-3.5 w-3.5" />
          Merge Gates
        </div>
        <GatesSection issueId={issueId} />
      </section>

      <div className="h-px bg-border" />

      <section className="space-y-2">
        <div className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
          <FlaskConical className="h-3.5 w-3.5" />
          QA Runs
        </div>
        <VerdictSection issueId={issueId} />
      </section>
    </div>
  );
}
