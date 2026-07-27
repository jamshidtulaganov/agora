// SDLC stage pipeline — derived, never stored.
//
// There is no `stage` column on Issue. The issue's position in the
// Build -> Review cycle is derived client-side from signals that already
// exist (status, labels, PR/merge state, running task attribution). This
// keeps the pipeline a pure projection of data the backend already owns — no
// new source of truth, no migration.
//
// TWO stages, because there are exactly two owners: an agent BUILDS, a human
// REVIEWS and merges. Everything the machine does before the human looks is
// "dev"; everything from "a human must now decide" onward is "review".
//
// QA is NOT a stage in this pipeline. It used to be the middle beat, but QA
// no longer runs automatically on the way to review — it is an ON-DEMAND
// action (the Release page, the QA lens, the manual `run_qa` slice action),
// so it is not a gate the issue walks through and not a dot the user waits
// on. Its verdict labels (qa:pass / qa:fail) remain ADVISORY signal surfaced
// by the review lens and the merge-readiness endpoint: a qa:fail still blocks
// (a known-red gate should never be merged over), a never-run QA never does.
//
// Design is NOT a stage either. For Agora's ICP (small vibe-coding dev teams,
// usually without a dedicated designer), design is an INPUT to the dev build
// — not a co-equal SDLC stage with its own reviewer ceremony. A Figma link on
// an issue is injected as context into the dev build task (see
// sliceActionDraftCode in server/internal/handler/slice_action.go), and the
// design lens/machinery stays available as an OPTIONAL, deep-linkable view
// (`?lens=design`, see packages/views/issues/lens.ts) for teams that want it.
//
// Deploy is also NOT part of this pipeline. It used to be a stage here, but
// deploy is a SPRINT-level concern (a shared branch deployed as a cycle, not
// a per-issue checkbox) — it now lives in the sprint-readiness view
// (packages/views/qa/components/qa-sprint-readiness-view.tsx). See
// docs/deploy-mcp-integration.md for the deploy cycle's own design.
//
// See docs/sdlc-stage-cockpit-plan.md section 2 for the design rationale.
//
// `status` on the input is a raw string (not `IssueStatus`) because it comes
// straight off an API response. Per the API Response Compatibility rules in
// CLAUDE.md, enum drift must downgrade rather than crash: an unrecognized
// status string is treated as "todo".

import { ALL_STATUSES } from "./config/status";
import type { IssueStatus } from "../types/issue";

export type SDLCStage = "dev" | "review";

export type StageState =
  | "pending"
  | "active"
  | "running"
  | "passed"
  | "failed"
  | "blocked"
  | "skipped";

export interface StageSnapshot {
  stage: SDLCStage;
  state: StageState;
  detail?: string;
  /**
   * The id of the agent task currently running THIS stage, when the caller
   * attributed a running task to it (see StagePipelineInput.runningTaskByStage).
   * Lets the stepper bind the stage to its live agent process — the trailing
   * narrative reads the newest activity step off it, and clicking the stage
   * opens a live "what the agent is doing now" popover. Absent when no task is
   * running this stage.
   */
  taskId?: string;
}

export interface StagePipeline {
  stages: StageSnapshot[];
  current: SDLCStage;
}

export interface StagePipelineInput {
  /** Raw status string off an API response; unknown values are treated as "todo". */
  status: string;
  labels: { name: string }[];
  prNumber?: number | null;
  /**
   * True when a merge gate has explicitly FAILED (a red qa/ci/review verdict).
   * Gates that are merely *pending* never reach this flag: nothing machine-run
   * is required before a human may review, so a gate that never ran must not
   * park the review stage. See computeMergeReadiness in
   * server/internal/handler/merge_readiness.go.
   */
  gateFailed?: boolean;
  prMerged?: boolean;
  /** Stages a currently-running agent task is attributed to (caller-derived). */
  runningTaskStages?: SDLCStage[];
  /**
   * The running agent task id per stage (caller-derived, first task wins).
   * Carries the "which run" alongside runningTaskStages' "which stage" so the
   * stepper can bind a stage to its live agent process. When present, its keys
   * are treated as running stages too (so runningTaskStages can be omitted).
   */
  runningTaskByStage?: Partial<Record<SDLCStage, string>>;
}

const STAGE_ORDER: SDLCStage[] = ["dev", "review"];

const KNOWN_STATUSES = new Set<string>(ALL_STATUSES);

/** Enum-drift guard: any status the frontend doesn't recognize downgrades to "todo". */
function normalizeStatus(status: string): IssueStatus {
  return (KNOWN_STATUSES.has(status) ? status : "todo") as IssueStatus;
}

function deriveDevStage(
  input: StagePipelineInput,
  status: IssueStatus,
  running: Set<SDLCStage>,
): StageSnapshot {
  const hasPr = input.prNumber !== undefined && input.prNumber !== null;

  let state: StageState;
  if (hasPr || status === "in_review" || status === "done") {
    state = "passed";
  } else if (running.has("dev")) {
    state = "running";
  } else if (status === "blocked") {
    state = "blocked";
  } else {
    state = "pending";
  }

  return { stage: "dev", state };
}

// Review = "a human now owns this". The stage opens the moment the work is
// ready to look at (in_review) and closes when the PR merges. An optional
// agent reviewer may run first and land review:pass / review:fail
// (server-captured from the ```review-result``` block); a human's Approve &
// merge decision stamps merge:approved. Signal precedence, strongest first:
//   merged/done > merge:override > merge:approved ("approved", the dispatch is
//   out but the PR hasn't merged yet) > review:fail > a FAILED merge gate >
//   a reviewer agent running > review:pass ("awaiting approval") > in_review
//   (awaiting the human) > pending.
//
// A merely PENDING gate never parks this stage: no machine verdict is required
// before a human may review, so "QA never ran" reads as ready-for-you, not as
// a wait. Only an explicit red gate (gateFailed) stops it.
function deriveReviewStage(
  input: StagePipelineInput,
  status: IssueStatus,
  labelNames: Set<string>,
  running: Set<SDLCStage>,
): StageSnapshot {
  const overrideLabel = labelNames.has("merge:override");

  let state: StageState;
  let detail: string | undefined;

  if (input.prMerged === true || status === "done") {
    state = "passed";
  } else if (overrideLabel) {
    state = "passed";
    detail = "override";
  } else if (labelNames.has("merge:approved")) {
    // Approval and execution are distinct. The backend can record approval
    // even when no orchestrator resolves and the human must merge manually,
    // so do not claim that a merge is in progress until the PR is actually
    // observed as merged.
    state = "active";
    detail = "approved";
  } else if (labelNames.has("review:fail")) {
    state = "failed";
  } else if (input.gateFailed === true) {
    state = "failed";
  } else if (running.has("review")) {
    // A reviewer agent is executing on this issue right now — no verdict label
    // has landed yet, so the stage reads "running" (the breathing dot) and can
    // bind to that run's live process, just like dev.
    state = "running";
  } else if (labelNames.has("review:pass")) {
    // The optional agent reviewer signed off — the stage now waits on the HUMAN.
    state = "active";
    detail = "awaiting approval";
  } else if (status === "in_review") {
    state = "active";
  } else {
    state = "pending";
  }

  // No resting tier chip: "trivial/light/full" is internal gate-policy jargon,
  // not something the stepper beat should ever surface. Only the meaningful
  // detail states above (override / merging… / awaiting approval) show.

  const snapshot: StageSnapshot = { stage: "review", state };
  if (detail !== undefined) {
    snapshot.detail = detail;
  }
  return snapshot;
}

/**
 * Resolves `current` from a finalized (post status==="done" forcing) list of
 * stage snapshots, promoting the current stage from "pending" to "active" in
 * the returned copy. Cancelled issues never get the pending->active
 * promotion — the stepper should show the pipeline as it actually stalled.
 */
function finalizePipeline(stages: StageSnapshot[], status: IssueStatus): StagePipeline {
  const target = stages.find((s) => s.state !== "passed" && s.state !== "skipped");

  if (!target) {
    // Every stage is passed/skipped (or status forced them there): pin
    // current to the last non-skipped stage. No stage in this 2-stage model
    // normally derives to "skipped" (skipped stays a valid StageState for
    // future use), so this fallback is effectively unreachable — kept for
    // defensiveness (fuzz-tested).
    const lastNonSkipped = [...stages].reverse().find((s) => s.state !== "skipped");
    const fallback: StageSnapshot = { stage: "review", state: "pending" };
    return { stages, current: (lastNonSkipped ?? fallback).stage };
  }

  if (status !== "cancelled" && target.state === "pending") {
    // Reference equality is safe here: `target` is the exact array element
    // `.find` returned, and every StageSnapshot in `stages` is a distinct
    // object built fresh by the derive* functions above.
    const promoted = stages.map((s) =>
      s === target ? { ...s, state: "active" as StageState } : s,
    );
    return { stages: promoted, current: target.stage };
  }

  return { stages, current: target.stage };
}

export function deriveStagePipeline(input: StagePipelineInput): StagePipeline {
  const status = normalizeStatus(input.status);
  const labelNames = new Set(input.labels.map((l) => l.name));
  const taskByStage = input.runningTaskByStage ?? {};
  // Running stages come from the explicit list, plus any stage the taskId map
  // names (so a caller can pass only the map). Union keeps both callers working.
  const running = new Set<SDLCStage>([
    ...(input.runningTaskStages ?? []),
    ...(Object.keys(taskByStage) as SDLCStage[]),
  ]);

  const dev = deriveDevStage(input, status, running);
  const review = deriveReviewStage(input, status, labelNames, running);

  let stages: StageSnapshot[] = STAGE_ORDER.map((stage) => ({ dev, review })[stage]);

  // Bind each stage to its running task id (if any). Done before the done/finalize
  // passes, which spread-copy snapshots and preserve the field.
  stages = stages.map((s) => {
    const taskId = taskByStage[s.stage];
    return taskId ? { ...s, taskId } : s;
  });

  if (status === "done") {
    stages = stages.map((s) => (s.state === "skipped" ? s : { ...s, state: "passed" }));
  }

  return finalizePipeline(stages, status);
}
