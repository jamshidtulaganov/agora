// QA evidence — the durable, evidence-first QA verdict for an issue. The server
// parses a run_qa comment's ```qa-result``` block and persists ONE row; the
// issue's QA section reads it (a single indexed query) instead of re-parsing the
// timeline, so opening any of N in-review tasks is cheap. See migration 134.

// One command the run_qa smoke ran, with baseline-vs-branch exit codes and the
// diff classification (new_failure is the only kind that blocks the gate).
export interface QACommand {
  cmd: string;
  baseline_exit: number | null;
  // Nullable, symmetric with baseline_exit — null when the command ran on the
  // baseline side only (a real run_qa agent emits this).
  branch_exit: number | null;
  kind: "pass" | "new_failure" | "pre_existing";
  // Short failure reason (stderr tail / assertion message) the agent reports
  // for a non-passing command — empty for "pass". The deterministic exit code
  // is still the source of truth for pass/fail; this is WHY, not a re-judgment.
  error: string;
}

// One design mismatch — an implemented value that diverges from the Figma
// reference / design-manifest token. Deterministic (getComputedStyle / DOM),
// never a pixel diff.
export interface QADesignMismatch {
  kind: "color" | "typography" | "spacing" | "layout" | "missing_element" | "other";
  selector: string;
  expected: string;
  actual: string;
}

// One diff-scoped design-system lint finding: something the CHANGE introduced
// that erodes the design system (a hardcoded value where a token exists, a
// duplicate of an existing component).
export interface QADesignLint {
  kind: "off_token" | "duplicate_component" | "other";
  where: string;
  issue: string;
  severity: "warn" | "block";
}

// Advisory design-verification result for a design-implementing issue. `skipped`
// (Figma unreachable) never fails the gate.
export interface QADesignResult {
  verdict: "pass" | "fail" | "skipped";
  reference_node: string;
  mismatches: QADesignMismatch[];
  // Diff-scoped design-system lint findings (present on manifest projects).
  lint?: QADesignLint[];
}

// The structured payload from the ```qa-result``` block: the verdict + command
// table + captured screenshots. Stored verbatim as qa_evidence.result_json.
export interface QAResult {
  verdict: "pass" | "fail";
  summary: string;
  commands: QACommand[];
  screenshots: string[];
  // Present only for design-implementing issues (design-aware QA).
  design?: QADesignResult | null;
}

// A persisted evidence row. result is the parsed QAResult (null only if a future
// row stored a malformed payload — treat defensively). baseline_ref / branch_sha
// are empty in P1 (one latest row per issue); P2 fills them for per-commit history.
export interface QAEvidence {
  id: string;
  issue_id: string;
  baseline_ref: string;
  branch_sha: string;
  verdict: string;
  // Who produced this verdict: "agent" (run_qa), "human", or machinery
  // ("watchdog"/"system"). Older servers omit it — treat "" as agent.
  source?: string;
  summary: string;
  result: QAResult | null;
  captured_at: string;
  // The server-computed single source of truth (service.ReconcileQAState on
  // the backend): "running" | "pass" | "fail" | "blocked" | "stale" |
  // "never_ran" | "pass_with_failing_cases", or "" when the server predates
  // this field — consumers must fall back to their own label-derived
  // computation on "" (or any value they don't recognize).
  reconciled_state: string;
  // Run identity (Phase 3, migration 157). All degrade to "" on old servers.
  commit_sha: string; // sha the verdict judged; "" = unreported/legacy
  triggered_by: string; // agent | human | auto; "" = legacy
  started_at: string; // RFC3339 or ""
  finished_at: string; // RFC3339 or ""
}
