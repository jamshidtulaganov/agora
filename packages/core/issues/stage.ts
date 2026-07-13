// SDLC stage pipeline — derived, never stored.
//
// There is no `stage` column on Issue. The issue's position in the
// Dev -> QA -> Review cycle is derived client-side from signals that already
// exist (status, labels, PR/merge state, running task attribution). This
// keeps the pipeline a pure projection of data the backend already owns — no
// new source of truth, no migration.
//
// Design is NOT a stage in this pipeline. For Agora's ICP (small vibe-coding
// dev teams, usually without a dedicated designer), design is an INPUT to the
// dev build — not a co-equal SDLC stage with its own reviewer ceremony. A
// Figma link on an issue is injected as context into the dev build task (see
// sliceActionDraftCode in server/internal/handler/slice_action.go), and the
// design lens/machinery stays available as an OPTIONAL, deep-linkable view
// (`?lens=design`, see packages/views/issues/lens.ts) for teams that want it
// — it's just no longer a stepper stage the user clicks through.
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
import type { WorkMode } from "./work-mode";

export type SDLCStage = "dev" | "qa" | "review";

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

export type MergeGateState = "pass" | "fail" | "pending";

export interface StagePipelineInput {
  /** Raw status string off an API response; unknown values are treated as "todo". */
  status: string;
  labels: { name: string }[];
  workMode?: WorkMode;
  prNumber?: number | null;
  mergeGates?: { ci: MergeGateState; qa: MergeGateState; tier: string } | null;
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

const STAGE_ORDER: SDLCStage[] = ["dev", "qa", "review"];

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

  const snapshot: StageSnapshot = { stage: "dev", state };
  if (input.workMode === "in_editor" && (state === "pending" || state === "running")) {
    snapshot.detail = "in editor";
  }
  return snapshot;
}

function deriveQaStage(
  status: IssueStatus,
  labelNames: Set<string>,
  running: Set<SDLCStage>,
): StageSnapshot {
  let state: StageState;
  if (labelNames.has("qa:pass") || status === "done") {
    state = "passed";
  } else if (labelNames.has("qa:fail")) {
    state = "failed";
  } else if (labelNames.has("qa:blocked")) {
    state = "blocked";
  } else if (running.has("qa")) {
    state = "running";
  } else if (status === "in_review") {
    state = "active";
  } else {
    state = "pending";
  }

  const snapshot: StageSnapshot = { stage: "qa", state };
  if (labelNames.has("qa:stale") && state !== "passed") {
    snapshot.detail = "stale";
  }
  return snapshot;
}

// Review stage v2 ("agent reviews, human approves"): the reviewer agent's
// verdict lands as review:pass / review:fail labels (server-captured from the
// ```review-result``` block), and a human's Approve & merge decision stamps
// merge:approved. Signal precedence, strongest first:
//   merged/done > merge:override > merge:approved ("merging…", the dispatch
//   is out but the PR hasn't merged yet) > review:fail > ci/qa gate fail >
//   review:pass with QA passed ("awaiting approval") > pending-gates active >
//   pending.
function deriveReviewStage(
  input: StagePipelineInput,
  status: IssueStatus,
  labelNames: Set<string>,
  qaStage: StageSnapshot,
  running: Set<SDLCStage>,
): StageSnapshot {
  const overrideLabel = labelNames.has("merge:override");
  const gates = input.mergeGates ?? null;

  let state: StageState;
  let detail: string | undefined;

  if (input.prMerged === true || status === "done") {
    state = "passed";
  } else if (overrideLabel) {
    state = "passed";
    detail = "override";
  } else if (labelNames.has("merge:approved")) {
    // A human approved; the merge order is dispatched but the PR isn't
    // merged yet (prMerged would have won above once it lands).
    state = "active";
    detail = "merging…";
  } else if (labelNames.has("review:fail")) {
    state = "failed";
  } else if (gates !== null && (gates.ci === "fail" || gates.qa === "fail")) {
    state = "failed";
  } else if (running.has("review")) {
    // A reviewer agent is executing on this issue right now — no verdict label
    // has landed yet, so the stage reads "running" (the breathing dot) and can
    // bind to that run's live process, just like dev/qa.
    state = "running";
  } else if (labelNames.has("review:pass") && qaStage.state === "passed") {
    // Both machine gates are green — the stage now waits on the HUMAN.
    state = "active";
    detail = "awaiting approval";
  } else if (
    gates !== null &&
    (gates.ci === "pending" || gates.qa === "pending") &&
    qaStage.state === "passed"
  ) {
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
    // current to the last non-skipped stage. No stage in this 3-stage model
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
  const qa = deriveQaStage(status, labelNames, running);
  const review = deriveReviewStage(input, status, labelNames, qa, running);

  let stages: StageSnapshot[] = STAGE_ORDER.map(
    (stage) => ({ dev, qa, review })[stage],
  );

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
