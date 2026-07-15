// Review verdict — the latest run_review code-review result for an issue
// (GET /api/issues/{id}/review-verdict). There is deliberately no review
// table on the backend: the reviewer agent's ```review-result``` comment IS
// the record, and the endpoint resolves the newest parsable block. The
// Review lens reads this to render the verdict card + findings list, and the
// human decision endpoint (POST /api/issues/{id}/review-decision) closes the
// loop ("agent reviews, human approves").

// One reviewer finding. `severity` is "blocker" | "major" | "minor" as the
// reviewer emits it — typed as string because the block is agent-authored
// and an unrecognized value must degrade to a generic row, not crash.
export interface ReviewFinding {
  file: string;
  line: number | null;
  severity: string;
  title: string;
  detail: string;
}

// The review-verdict payload. `verdict` is "pass" | "fail" | "none" ("none"
// = no captured review yet — every other field is then zero-valued and the
// lens renders the empty state). Kept as string for enum-drift tolerance.
export interface ReviewVerdict {
  verdict: string;
  summary: string;
  commit_sha: string;
  files_reviewed: number;
  findings: ReviewFinding[];
  comment_id: string;
  reviewed_at: string;
  reviewer_agent_id: string;
}

// POST /api/issues/{id}/review-decision response. The two actions return
// different shapes. request_changes returns the durable plan revision identity
// so the UI can confirm that work entered the orchestration DAG.
export interface ReviewDecisionResponse {
  action: string;
  merged_dispatch: boolean;
  status: string;
  dispatched: boolean;
  plan_version: number;
  revision_id: string;
  correction_step_id: string;
}
