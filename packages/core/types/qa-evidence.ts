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

// The structured payload from the ```qa-result``` block: the verdict + command
// table + captured screenshots. Stored verbatim as qa_evidence.result_json.
export interface QAResult {
  verdict: "pass" | "fail";
  summary: string;
  commands: QACommand[];
  screenshots: string[];
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
  summary: string;
  result: QAResult | null;
  captured_at: string;
}
