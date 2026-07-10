// Structured test-case steps — a canonical serialization for the existing
// `test_case.steps` text column (see migration 135). No schema change: the
// column stays a plain string; this module is the pure parse/serialize layer
// that lets the QA panel render/edit it as action → expects rows instead of
// one free-text blob.
//
// Canonical line format (one step per line):
//   `N. <action>`
//   `N. <action> → expects: <expectation>`
// The leading `N.` is UI chrome — a step's position in the array IS its
// number, so it is stripped on parse and regenerated on serialize, never
// preserved verbatim.
//
// parseSteps is deliberately permissive: it NEVER throws and NEVER drops a
// non-blank line. A line that doesn't match the canonical shape becomes
// `{ action: <the raw line> }` — this is what lets a legacy free-text `steps`
// blob (authored before this format existed, or a hand-typed paragraph)
// degrade into N action-only steps instead of failing to parse.

export interface ParsedStep {
  action: string;
  expects?: string;
}

// Leading "1. " / "2) " style numbering to strip before inspecting the body.
const STEP_NUMBER_RE = /^\d+[.)]\s*/;

// The canonical action/expects separator. Greedy on the action side so an
// arrow that appears INSIDE the action text itself (e.g. "Click Next →
// Confirm") stays part of the action — only the LAST "→ expects:" (or the
// ASCII "-> expects:" fallback) in the line is treated as the real split
// point. Case-insensitive so "Expects:" still matches.
const EXPECTS_SPLIT_RE = /^(.*)(?:→|->)\s*expects:\s*(.*)$/i;

export function parseSteps(text: string): ParsedStep[] {
  if (!text) return [];
  const steps: ParsedStep[] = [];
  for (const rawLine of text.split("\n")) {
    const trimmed = rawLine.trim();
    if (trimmed === "") continue; // blank lines carry no content — skipped
    const body = trimmed.replace(STEP_NUMBER_RE, "");
    const m = EXPECTS_SPLIT_RE.exec(body);
    if (m) {
      const action = (m[1] ?? "").trim();
      const expects = (m[2] ?? "").trim();
      if (action !== "") {
        steps.push(expects !== "" ? { action, expects } : { action });
        continue;
      }
      // Action side was empty (e.g. the whole line was "→ expects: foo") —
      // fall through and keep the raw body rather than losing the text.
    }
    steps.push({ action: body });
  }
  return steps;
}

export function serializeSteps(steps: ParsedStep[]): string {
  return steps
    .map((s, i) => {
      const action = s.action.trim();
      const expects = s.expects?.trim();
      const prefix = `${i + 1}. ${action}`;
      return expects ? `${prefix} → expects: ${expects}` : prefix;
    })
    .join("\n");
}
