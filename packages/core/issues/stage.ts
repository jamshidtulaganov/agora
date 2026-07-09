// SDLC stage pipeline — derived, never stored.
//
// There is no `stage` column on Issue. The issue's position in the
// Design -> Dev -> QA -> Review -> Deploy cycle is derived client-side from
// signals that already exist (status, labels, PR/merge state, running task
// attribution, deploy sync). This keeps the pipeline a pure projection of
// data the backend already owns — no new source of truth, no migration.
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

export type SDLCStage = "design" | "dev" | "qa" | "review" | "deploy";

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
  /** Figma refs or design evidence present on the issue. */
  hasDesignSignals: boolean;
  designVerdict?: "pass" | "fail" | null;
  /** QA verdict; a "pass" here also counts as a design-pass override. */
  qaVerdict?: "pass" | "fail" | null;
  mergeGates?: { ci: MergeGateState; qa: MergeGateState; tier: string } | null;
  prMerged?: boolean;
  /** Stages a currently-running agent task is attributed to (caller-derived). */
  runningTaskStages?: SDLCStage[];
  /** Project has a connected box / local dir to deploy to. */
  hasDeployTarget: boolean;
  deploySynced?: boolean;
  /** Optional detail shown on a passed deploy stage (e.g. the deployed ref) —
   *  caller-derived from the deploy_event signal, deploy P0. Purely cosmetic:
   *  never affects state, only StageSnapshot.detail when the stage passes. */
  deployDetail?: string;
}

const STAGE_ORDER: SDLCStage[] = ["design", "dev", "qa", "review", "deploy"];

const KNOWN_STATUSES = new Set<string>(ALL_STATUSES);

/** Enum-drift guard: any status the frontend doesn't recognize downgrades to "todo". */
function normalizeStatus(status: string): IssueStatus {
  return (KNOWN_STATUSES.has(status) ? status : "todo") as IssueStatus;
}

function deriveDesignStage(
  input: StagePipelineInput,
  status: IssueStatus,
  labelNames: Set<string>,
  running: Set<SDLCStage>,
): StageSnapshot {
  if (!input.hasDesignSignals) {
    return { stage: "design", state: "skipped" };
  }
  if (status === "done") {
    return { stage: "design", state: "passed" };
  }
  // design:pass/design:fail are the durable, board-filterable signal (backend
  // attaches them from qa-result.design.verdict — see
  // TaskService.captureDesignVerdictLabel, qa_evidence.go). Labels take
  // precedence over the raw verdict fields — including the qa:pass override,
  // so an explicit design:fail is never silently erased by a green QA gate.
  // The verdict fields remain as fallback for evidence captured before the
  // labels existed — no backfill needed.
  if (labelNames.has("design:fail")) {
    return { stage: "design", state: "failed" };
  }
  if (labelNames.has("design:pass")) {
    return { stage: "design", state: "passed" };
  }
  if (input.designVerdict === "fail") {
    return { stage: "design", state: "failed" };
  }
  if (input.designVerdict === "pass" || input.qaVerdict === "pass") {
    return { stage: "design", state: "passed" };
  }
  if (running.has("design")) {
    return { stage: "design", state: "running" };
  }
  return { stage: "design", state: "pending" };
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

function deriveReviewStage(
  input: StagePipelineInput,
  status: IssueStatus,
  labelNames: Set<string>,
  qaStage: StageSnapshot,
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
  } else if (gates !== null && (gates.ci === "fail" || gates.qa === "fail")) {
    state = "failed";
  } else if (
    gates !== null &&
    (gates.ci === "pending" || gates.qa === "pending") &&
    qaStage.state === "passed"
  ) {
    state = "active";
  } else {
    state = "pending";
  }

  if (detail === undefined && gates !== null) {
    detail = gates.tier;
  }

  const snapshot: StageSnapshot = { stage: "review", state };
  if (detail !== undefined) {
    snapshot.detail = detail;
  }
  return snapshot;
}

function deriveDeployStage(
  input: StagePipelineInput,
  running: Set<SDLCStage>,
): StageSnapshot {
  if (!input.hasDeployTarget) {
    return { stage: "deploy", state: "skipped" };
  }
  if (input.deploySynced === true) {
    const snapshot: StageSnapshot = { stage: "deploy", state: "passed" };
    if (input.deployDetail) {
      snapshot.detail = input.deployDetail;
    }
    return snapshot;
  }
  if (running.has("deploy")) {
    return { stage: "deploy", state: "running" };
  }
  return { stage: "deploy", state: "pending" };
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
    // current to the last non-skipped stage.
    const lastNonSkipped = [...stages].reverse().find((s) => s.state !== "skipped");
    const fallback: StageSnapshot = { stage: "deploy", state: "pending" };
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
  const running = new Set(input.runningTaskStages ?? []);

  const design = deriveDesignStage(input, status, labelNames, running);
  const dev = deriveDevStage(input, status, running);
  const qa = deriveQaStage(status, labelNames, running);
  const review = deriveReviewStage(input, status, labelNames, qa);
  const deploy = deriveDeployStage(input, running);

  let stages: StageSnapshot[] = STAGE_ORDER.map(
    (stage) => ({ design, dev, qa, review, deploy })[stage],
  );

  if (status === "done") {
    stages = stages.map((s) => (s.state === "skipped" ? s : { ...s, state: "passed" }));
  }

  return finalizePipeline(stages, status);
}
