// Per-step manual run results (QA lens phase 4) — the TestRail-style checklist
// walk of a case's structured steps (see ./steps.ts). One finished walk records
// ONE test_run row via the EXISTING human recording path (POST
// /api/test-cases/:id/runs); the per-step breakdown rides inside the run's
// free-text `output` column as a fenced ```step-results JSON array below a
// human-readable summary line, so agents / plain-text readers still get a
// one-line verdict while the QA panel parses the fence back into a structured
// checklist display. No schema change — output already holds arbitrary run
// detail.

export type StepResultStatus = "pass" | "fail" | "skip";

export interface StepResult {
  step: number; // 1-based position in the case's parsed steps
  status: StepResultStatus;
  note?: string; // actual result on a fail (optional)
}

const FENCE_OPEN = "```step-results";
const FENCE_CLOSE = "```";

// deriveStepRunVerdict maps a finished checklist to the CASE verdict the run
// records: any failing step fails the case; a walk where every step was
// skipped proves nothing ran ("skip", which the panel renders as blocked-ish);
// otherwise (≥1 pass, rest pass/skip) the case passes.
export function deriveStepRunVerdict(results: StepResult[]): "pass" | "fail" | "skip" {
  if (results.some((r) => r.status === "fail")) return "fail";
  if (results.length > 0 && results.every((r) => r.status === "skip")) return "skip";
  return "pass";
}

// serializeStepResults renders the run's `output` text: an English summary
// line (data, like agent-written outputs — never localized) plus the fenced
// JSON array the panel parses back.
export function serializeStepResults(results: StepResult[]): string {
  const passed = results.filter((r) => r.status === "pass").length;
  const failedSteps = results.filter((r) => r.status === "fail").map((r) => r.step);
  const skipped = results.filter((r) => r.status === "skip").length;
  let summary = `Manual step run — ${passed}/${results.length} passed`;
  if (failedSteps.length > 0) summary += `, failed at step ${failedSteps.join(", ")}`;
  if (skipped > 0) summary += `, ${skipped} skipped`;
  const json = JSON.stringify(
    results.map((r) => (r.note?.trim() ? { step: r.step, status: r.status, note: r.note.trim() } : { step: r.step, status: r.status })),
  );
  return `${summary}\n${FENCE_OPEN}\n${json}\n${FENCE_CLOSE}`;
}

// parseStepResults extracts the fenced breakdown back out of a run's output.
// Returns null (never throws) when the output carries no parsable fence — an
// agent run's free-text evidence, a legacy run, malformed JSON — so callers
// fall back to rendering the raw text.
export function parseStepResults(output: string): StepResult[] | null {
  if (!output) return null;
  const start = output.indexOf(FENCE_OPEN);
  if (start === -1) return null;
  const bodyStart = start + FENCE_OPEN.length;
  const end = output.indexOf(FENCE_CLOSE, bodyStart);
  if (end === -1) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(output.slice(bodyStart, end).trim());
  } catch {
    return null;
  }
  if (!Array.isArray(parsed)) return null;
  const results: StepResult[] = [];
  for (const item of parsed) {
    if (item === null || typeof item !== "object") continue;
    const { step, status, note } = item as { step?: unknown; status?: unknown; note?: unknown };
    if (typeof step !== "number" || !Number.isFinite(step)) continue;
    if (status !== "pass" && status !== "fail" && status !== "skip") continue;
    results.push(typeof note === "string" && note !== "" ? { step, status, note } : { step, status });
  }
  return results.length > 0 ? results : null;
}
