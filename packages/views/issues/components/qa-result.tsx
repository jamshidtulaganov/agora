"use client";

import { CheckCircle2, ChevronDown, CircleAlert, MinusCircle, Terminal } from "lucide-react";
import { cn } from "@agora/ui/lib/utils";
import type { QACommand, QAResult } from "@agora/core/types";
import { useT } from "../../i18n";

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
      title: typeof c.title === "string" ? c.title : "",
      expected: typeof c.expected === "string" ? c.expected : "",
      observed: typeof c.observed === "string" ? c.observed : "",
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

type QACommandLabel =
  | "reported"
  | "required_file"
  | "automated_case"
  | "browser"
  | "types"
  | "quality"
  | "build"
  | "backend"
  | "tests"
  | "automated";

interface QACommandDescription {
  key: QACommandLabel;
  reported?: string;
}

interface QACommandGroup {
  id: string;
  name: string;
  kind: QACommand["kind"];
  commands: QACommand[];
}

/**
 * Convert a command into a stable human concept. This deliberately does not
 * return the command itself as a fallback: a `/tmp/case-…mjs` path is useful
 * diagnostic evidence, but it is not a product-facing test name.
 */
export function describeQACommand(command: QACommand): QACommandDescription {
  const reported = command.title?.trim();
  if (reported) return { key: "reported", reported };

  const value = command.cmd.trim();
  const assertion = value.match(/^check:\s*(.+)$/i)?.[1]?.trim();
  if (assertion) {
    if (/\bexists(?:\s+with\s+exact\s+content)?\b/i.test(assertion)) {
      return { key: "required_file" };
    }
    return { key: "reported", reported: assertion };
  }
  if (/\/(?:private\/)?tmp\/case-[^\s/]+\.m?js\b/i.test(value)) return { key: "automated_case" };
  if (/playwright|cypress|browser|smoke/i.test(value)) return { key: "browser" };
  if (/typecheck|\btsc\b/i.test(value)) return { key: "types" };
  if (/\blint\b|eslint|golangci/i.test(value)) return { key: "quality" };
  if (/\bbuild\b/i.test(value)) return { key: "build" };
  if (/\bgo\s+test\b/i.test(value)) return { key: "backend" };
  if (/\b(?:pnpm|npm|yarn|bun)\b.*\btest\b|\bnode\s+--test\b|vitest|jest|pytest|phpunit/i.test(value)) {
    return { key: "tests" };
  }
  return { key: "automated" };
}

function commandName(
  description: QACommandDescription,
  t: ReturnType<typeof useT<"issues">>["t"],
): string {
  if (description.key === "reported") return description.reported ?? t(($) => $.qa_evidence.check_automated);
  switch (description.key) {
    case "required_file": return t(($) => $.qa_evidence.check_required_file);
    case "automated_case": return t(($) => $.qa_evidence.check_automated_case);
    case "browser": return t(($) => $.qa_evidence.check_browser);
    case "types": return t(($) => $.qa_evidence.check_types);
    case "quality": return t(($) => $.qa_evidence.check_quality);
    case "build": return t(($) => $.qa_evidence.check_build);
    case "backend": return t(($) => $.qa_evidence.check_backend);
    case "tests": return t(($) => $.qa_evidence.check_tests);
    default: return t(($) => $.qa_evidence.check_automated);
  }
}

function groupCommands(
  commands: QACommand[],
  t: ReturnType<typeof useT<"issues">>["t"],
): QACommandGroup[] {
  const groups = new Map<string, QACommandGroup>();
  for (const command of commands) {
    const description = describeQACommand(command);
    const name = commandName(description, t);
    const id = `${command.kind}:${name.toLocaleLowerCase()}`;
    const existing = groups.get(id);
    if (existing) existing.commands.push(command);
    else groups.set(id, { id, name, kind: command.kind, commands: [command] });
  }
  return [...groups.values()].sort((a, b) => {
    const rank = (kind: QACommand["kind"]) => kind === "new_failure" ? 0 : kind === "pre_existing" ? 1 : 2;
    return rank(a.kind) - rank(b.kind);
  });
}

function looksTechnical(value: string): boolean {
  return /\/(?:private\/)?tmp\/|\.(?:m?js|tsx?|go|php):\d+|\b(?:node|pnpm|npm|yarn|go test)\b|exit\s+code|stack trace/i.test(value);
}

function observedText(
  command: QACommand,
  t: ReturnType<typeof useT<"issues">>["t"],
): string {
  const reported = command.observed?.trim();
  const error = command.error?.trim();
  const value = reported || error;
  if (/file\s+not\s+found|not\s+found\s+on\s+any\s+branch/i.test(value)) {
    return t(($) => $.qa_evidence.observed_file_missing);
  }
  if (/timed?\s*out|timeout/i.test(value)) return t(($) => $.qa_evidence.observed_timeout);
  if (value && !looksTechnical(value)) return value;
  return t(($) => $.qa_evidence.observed_failed);
}

function expectedText(
  command: QACommand,
  t: ReturnType<typeof useT<"issues">>["t"],
): string {
  if (command.expected?.trim()) return command.expected.trim();
  return describeQACommand(command).key === "required_file"
    ? t(($) => $.qa_evidence.expected_file_present)
    : t(($) => $.qa_evidence.expected_check_pass);
}

function statusLabel(
  kind: QACommand["kind"],
  t: ReturnType<typeof useT<"issues">>["t"],
): string {
  if (kind === "new_failure") return t(($) => $.qa_evidence.check_failed);
  if (kind === "pre_existing") return t(($) => $.qa_evidence.check_preexisting);
  return t(($) => $.qa_evidence.check_passed);
}

function TechnicalCommandDetails({ commands }: { commands: QACommand[] }) {
  const { t } = useT("issues");
  return (
    <details className="group mt-3 border-t border-border/60 pt-2">
      <summary className="flex cursor-pointer list-none items-center gap-1.5 text-[11px] text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <ChevronDown className="size-3 transition-transform group-open:rotate-180 motion-reduce:transition-none" aria-hidden />
        <Terminal className="size-3" aria-hidden />
        {t(($) => $.qa_evidence.technical_details)}
      </summary>
      <div className="mt-2 space-y-2">
        {commands.map((command, index) => (
          <div key={`${command.cmd}-${index}`} className="rounded-md bg-muted/45 p-2 text-[10px] text-muted-foreground">
            <code className="block break-all font-mono text-foreground/80" translate="no">{command.cmd}</code>
            <div className="mt-1 flex gap-4 tabular-nums">
              <span>{t(($) => $.qa_evidence.baseline_result)}: {command.baseline_exit ?? "—"}</span>
              <span>{t(($) => $.qa_evidence.change_result)}: {command.branch_exit ?? "—"}</span>
            </div>
            {command.error && <pre className="mt-1 whitespace-pre-wrap break-words text-destructive/80">{command.error}</pre>}
          </div>
        ))}
      </div>
    </details>
  );
}

// Human-readable QA report. Test intent and failure explanations lead; raw
// shell commands and exit codes remain available under Technical details.
export function StructuredResult({ result }: { result: QAResult }) {
  const { t } = useT("issues");
  const groups = groupCommands(result.commands, t);
  const failures = groups.filter((group) => group.kind === "new_failure");
  const settled = groups.filter((group) => group.kind !== "new_failure");
  const settledCount = settled.reduce((count, group) => count + group.commands.length, 0);

  return (
    <section className="space-y-3 px-3 py-3" aria-label={t(($) => $.qa_evidence.automated_checks)}>
      <div className="flex items-center gap-2">
        <h3 className="text-xs font-medium">{t(($) => $.qa_evidence.automated_checks)}</h3>
        <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] tabular-nums text-muted-foreground">
          {t(($) => $.qa_evidence.checks_count, { count: result.commands.length })}
        </span>
      </div>

      {failures.map((group) => {
        const first = group.commands[0]!;
        return (
          <article key={group.id} className="rounded-lg border border-destructive/30 bg-destructive/[0.035] p-3">
            <div className="flex items-start gap-2">
              <CircleAlert className="mt-0.5 size-4 shrink-0 text-destructive" aria-hidden />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                  <h4 className="text-xs font-medium text-foreground">{group.name}</h4>
                  <span className="text-[10px] font-medium text-destructive">{statusLabel(group.kind, t)}</span>
                </div>
                <p className="mt-1 text-[11px] text-muted-foreground">
                  {t(($) => $.qa_evidence.introduced_failure)}
                </p>
                <dl className="mt-3 grid gap-2 sm:grid-cols-2">
                  <div className="rounded-md border border-border/60 bg-background/70 p-2.5">
                    <dt className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                      {t(($) => $.qa_evidence.expected)}
                    </dt>
                    <dd className="mt-1 text-[11px] text-foreground/90">{expectedText(first, t)}</dd>
                  </div>
                  <div className="rounded-md border border-destructive/20 bg-background/70 p-2.5">
                    <dt className="text-[10px] font-medium uppercase tracking-wide text-destructive/80">
                      {t(($) => $.qa_evidence.observed)}
                    </dt>
                    <dd className="mt-1 text-[11px] text-foreground/90">{observedText(first, t)}</dd>
                  </div>
                </dl>
                <TechnicalCommandDetails commands={group.commands} />
              </div>
            </div>
          </article>
        );
      })}

      {settled.length > 0 && (
        <details className="group rounded-lg border border-border/60 bg-muted/15">
          <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2.5 text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring">
            <CheckCircle2 className="size-4 shrink-0 text-emerald-600 dark:text-emerald-400" aria-hidden />
            <span>{t(($) => $.qa_evidence.settled_checks, { count: settledCount })}</span>
            <ChevronDown className="ml-auto size-3.5 text-muted-foreground transition-transform group-open:rotate-180 motion-reduce:transition-none" aria-hidden />
          </summary>
          <ul className="border-t border-border/60 px-3 py-2">
            {settled.map((group) => (
              <li key={group.id} className="flex items-start gap-2 py-1.5 text-[11px]">
                {group.kind === "pre_existing" ? (
                  <MinusCircle className="mt-px size-3.5 shrink-0 text-muted-foreground" aria-hidden />
                ) : (
                  <CheckCircle2 className="mt-px size-3.5 shrink-0 text-emerald-600 dark:text-emerald-400" aria-hidden />
                )}
                <span className="min-w-0 flex-1 text-foreground/85">{group.name}</span>
                {group.commands.length > 1 && (
                  <span className="shrink-0 tabular-nums text-muted-foreground">×{group.commands.length}</span>
                )}
                <span className={cn("shrink-0", group.kind === "pre_existing" ? "text-muted-foreground" : "text-emerald-600 dark:text-emerald-400") }>
                  {statusLabel(group.kind, t)}
                </span>
              </li>
            ))}
          </ul>
          <div className="px-3 pb-3"><TechnicalCommandDetails commands={settled.flatMap((group) => group.commands)} /></div>
        </details>
      )}

      {result.screenshots.length > 0 && (
        <details className="group rounded-md border border-border/60 px-3 py-2 text-[11px]">
          <summary className="flex cursor-pointer list-none items-center gap-2 text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
            <ChevronDown className="size-3 transition-transform group-open:rotate-180 motion-reduce:transition-none" aria-hidden />
            {t(($) => $.qa_evidence.screenshots_count, { count: result.screenshots.length })}
          </summary>
          <ul className="mt-2 space-y-1">
            {result.screenshots.map((s, i) => (
              <li key={i} className="truncate font-mono text-[10px] text-foreground/60" title={s} translate="no">{s}</li>
            ))}
          </ul>
        </details>
      )}
    </section>
  );
}
