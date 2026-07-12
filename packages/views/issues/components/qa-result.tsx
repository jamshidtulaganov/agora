/* eslint-disable i18next/no-literal-string -- QA cockpit internal; i18n follow-up */
"use client";

import { Fragment } from "react";
import { cn } from "@agora/ui/lib/utils";
import type { QACommand, QAResult } from "@agora/core/types";

// Shared QA-result contract helpers. The run_qa recipe appends a fenced
// ```qa-result JSON block to its verdict comment; parseQAResultBlock validates
// it defensively (agent-authored content — parse, don't trust) and
// StructuredResult renders the command table. Used by the issue QA evidence
// section, the QA review page, and the file-bug sheet, so all three render the
// SAME shape. (The editor's Tests tab that once lived beside these was removed
// — merge gates render in the review bar via EditorGates instead.)

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
