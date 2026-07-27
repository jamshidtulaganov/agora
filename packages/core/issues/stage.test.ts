import { describe, it, expect } from "vitest";
import {
  deriveStagePipeline,
  type StagePipelineInput,
  type StagePipeline,
  type SDLCStage,
} from "./stage";

/** Minimal valid input; every test overrides only what it needs. */
function baseInput(overrides: Partial<StagePipelineInput> = {}): StagePipelineInput {
  return {
    status: "todo",
    labels: [],
    ...overrides,
  };
}

function find(pipeline: StagePipeline, stage: SDLCStage) {
  const found = pipeline.stages.find((s) => s.stage === stage);
  if (!found) throw new Error(`stage ${stage} missing from pipeline`);
  return found;
}

describe("deriveStagePipeline — stage order", () => {
  it("always returns the two stages in dev/review order", () => {
    const pipeline = deriveStagePipeline(baseInput());
    expect(pipeline.stages.map((s) => s.stage)).toEqual(["dev", "review"]);
  });

  it("never emits a qa stage — QA left the pipeline as an on-demand action", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ status: "in_review", labels: [{ name: "qa:pass" }] }),
    );
    expect(pipeline.stages.some((s) => (s.stage as string) === "qa")).toBe(false);
  });
});

describe("deriveStagePipeline — dev stage", () => {
  it("passes once a PR is opened", () => {
    const pipeline = deriveStagePipeline(baseInput({ prNumber: 42 }));
    expect(find(pipeline, "dev").state).toBe("passed");
  });

  it("passes when status has advanced to in_review", () => {
    const pipeline = deriveStagePipeline(baseInput({ status: "in_review" }));
    expect(find(pipeline, "dev").state).toBe("passed");
  });

  it("passes when status is done", () => {
    const pipeline = deriveStagePipeline(baseInput({ status: "done" }));
    expect(find(pipeline, "dev").state).toBe("passed");
  });

  it("is running when a dev task is attributed as running", () => {
    const pipeline = deriveStagePipeline(baseInput({ runningTaskStages: ["dev"] }));
    expect(find(pipeline, "dev").state).toBe("running");
  });

  it("is blocked when the issue status is blocked", () => {
    const pipeline = deriveStagePipeline(baseInput({ status: "blocked" }));
    expect(find(pipeline, "dev").state).toBe("blocked");
  });

  it("is pending with no PR, not running, not blocked", () => {
    // dev is stage index 0, so a non-cancelled status would promote a
    // pending base state to "active". Use cancelled to observe the raw
    // base state.
    const pipeline = deriveStagePipeline(baseInput({ status: "cancelled" }));
    expect(find(pipeline, "dev").state).toBe("pending");
  });

  it("precedence: an opened PR wins over a blocked status", () => {
    const pipeline = deriveStagePipeline(baseInput({ prNumber: 7, status: "blocked" }));
    expect(find(pipeline, "dev").state).toBe("passed");
  });
});

describe("deriveStagePipeline — review stage", () => {
  it("passes once the PR is merged", () => {
    const pipeline = deriveStagePipeline(baseInput({ prMerged: true }));
    expect(find(pipeline, "review").state).toBe("passed");
  });

  it("passes when the issue is done", () => {
    const pipeline = deriveStagePipeline(baseInput({ status: "done" }));
    expect(find(pipeline, "review").state).toBe("passed");
  });

  it("passes via a merge:override label and records an 'override' detail", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ labels: [{ name: "merge:override" }] }),
    );
    expect(find(pipeline, "review")).toMatchObject({ state: "passed", detail: "override" });
  });

  it("fails when a merge gate has explicitly failed", () => {
    const pipeline = deriveStagePipeline(baseInput({ gateFailed: true }));
    expect(find(pipeline, "review").state).toBe("failed");
  });

  it("is active — not parked — while in_review with no gate signal at all", () => {
    // This is the whole point of the two-stage model: nothing machine-run is a
    // precondition for a human looking at the diff, so "no QA ever ran" reads
    // as ready-for-you rather than as a wait.
    const pipeline = deriveStagePipeline(baseInput({ status: "in_review" }));
    expect(find(pipeline, "review")).toEqual({ stage: "review", state: "active" });
  });

  it("stays active in_review even when a QA verdict label is absent entirely", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ status: "in_review", prNumber: 3, gateFailed: false }),
    );
    expect(find(pipeline, "review").state).toBe("active");
  });

  it("is pending with no merge signals at all and not yet in review", () => {
    const pipeline = deriveStagePipeline(baseInput({ status: "cancelled" }));
    expect(find(pipeline, "review")).toEqual({ stage: "review", state: "pending" });
  });

  it("never surfaces a tier detail on a failed gate", () => {
    const pipeline = deriveStagePipeline(baseInput({ gateFailed: true }));
    expect(find(pipeline, "review")).toEqual({ stage: "review", state: "failed" });
  });

  it("precedence: the override detail wins over a failed gate", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ labels: [{ name: "merge:override" }], gateFailed: true }),
    );
    expect(find(pipeline, "review")).toMatchObject({ state: "passed", detail: "override" });
  });

  it("precedence: a merged PR wins over a failing gate", () => {
    const pipeline = deriveStagePipeline(baseInput({ prMerged: true, gateFailed: true }));
    expect(find(pipeline, "review").state).toBe("passed");
  });
});

describe("deriveStagePipeline — review stage verdict labels", () => {
  it("fails on a review:fail label", () => {
    const pipeline = deriveStagePipeline(baseInput({ labels: [{ name: "review:fail" }] }));
    expect(find(pipeline, "review").state).toBe("failed");
  });

  it("review:fail wins over an otherwise-green pipeline", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ status: "in_review", labels: [{ name: "review:fail" }], gateFailed: false }),
    );
    expect(find(pipeline, "review")).toEqual({ stage: "review", state: "failed" });
  });

  it("is running while a reviewer agent is executing", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ status: "in_review", runningTaskStages: ["review"] }),
    );
    expect(find(pipeline, "review").state).toBe("running");
  });

  it("is active 'awaiting approval' on review:pass, with no QA precondition", () => {
    // An agent reviewer signed off. Previously this ALSO required the qa stage
    // to have passed; QA is no longer a stage, so the sign-off alone parks the
    // pipeline on the human.
    const pipeline = deriveStagePipeline(baseInput({ labels: [{ name: "review:pass" }] }));
    expect(find(pipeline, "review")).toMatchObject({
      state: "active",
      detail: "awaiting approval",
    });
  });

  it("is active 'approved' once merge:approved is stamped", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ labels: [{ name: "merge:approved" }] }),
    );
    expect(find(pipeline, "review")).toMatchObject({ state: "active", detail: "approved" });
  });

  it("precedence: merge:approved wins over review:fail (the human decided)", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ labels: [{ name: "merge:approved" }, { name: "review:fail" }] }),
    );
    expect(find(pipeline, "review")).toMatchObject({ state: "active", detail: "approved" });
  });

  it("precedence: merge:override wins over merge:approved", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ labels: [{ name: "merge:override" }, { name: "merge:approved" }] }),
    );
    expect(find(pipeline, "review")).toMatchObject({ state: "passed", detail: "override" });
  });

  it("precedence: a merged PR wins over merge:approved and review labels", () => {
    const pipeline = deriveStagePipeline(
      baseInput({
        prMerged: true,
        labels: [{ name: "merge:approved" }, { name: "review:fail" }],
      }),
    );
    expect(find(pipeline, "review").state).toBe("passed");
  });

  it("precedence: review:fail wins over a failed gate (the specific reason shows)", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ labels: [{ name: "review:fail" }], gateFailed: true }),
    );
    expect(find(pipeline, "review").state).toBe("failed");
  });

  it("status done forces review passed even with review:fail present", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ status: "done", labels: [{ name: "review:fail" }] }),
    );
    expect(find(pipeline, "review").state).toBe("passed");
  });

  it("ignores qa:* labels entirely — they are merge-gate signal, not stage state", () => {
    const withQaFail = deriveStagePipeline(
      baseInput({ status: "in_review", labels: [{ name: "qa:fail" }] }),
    );
    const withoutLabels = deriveStagePipeline(baseInput({ status: "in_review" }));
    // A raw qa:fail label alone changes nothing; only the resolved gateFailed
    // flag (computed server-side from the reconciled QA state) does.
    expect(withQaFail).toEqual(withoutLabels);
  });
});

describe("deriveStagePipeline — running attribution is per-stage", () => {
  it("only marks the stages the caller attributed as running", () => {
    // dev is stage index 0, so a non-cancelled status promotes its pending
    // base state to "active" (the current-stage promotion) — use cancelled
    // to observe that "review" is the only stage marked "running".
    const pipeline = deriveStagePipeline(
      baseInput({ status: "cancelled", runningTaskStages: ["review"] }),
    );
    expect(find(pipeline, "review").state).toBe("running");
    expect(find(pipeline, "dev").state).toBe("pending"); // not attributed, stays pending
  });
});

describe("deriveStagePipeline — current stage resolution", () => {
  it("promotes only the current stage from pending to active", () => {
    const pipeline = deriveStagePipeline(baseInput());
    // dev is the first open (non-passed) stage.
    expect(pipeline.current).toBe("dev");
    expect(find(pipeline, "dev").state).toBe("active");
    // review is also pending in this scenario but is NOT current, so it must
    // stay pending rather than also being promoted.
    expect(find(pipeline, "review").state).toBe("pending");
  });

  it("does not promote a non-pending current stage (e.g. blocked)", () => {
    const pipeline = deriveStagePipeline(baseInput({ status: "blocked" }));
    expect(pipeline.current).toBe("dev");
    expect(find(pipeline, "dev").state).toBe("blocked");
  });

  it("does not re-promote a stage whose base state is already active", () => {
    // status in_review => dev auto-passes; review lands on its own "active"
    // via the in_review branch (not via current-promotion).
    const pipeline = deriveStagePipeline(baseInput({ status: "in_review" }));
    expect(pipeline.current).toBe("review");
    expect(find(pipeline, "review").state).toBe("active");
  });

  it("advances current past every passed stage", () => {
    const pipeline = deriveStagePipeline(baseInput({ prNumber: 1 }));
    // dev passed -> review is first open.
    expect(pipeline.current).toBe("review");
    expect(find(pipeline, "review").state).toBe("active"); // promoted from pending
  });
});

describe("deriveStagePipeline — done status", () => {
  it("forces every stage to passed and pins current to the last stage", () => {
    const pipeline = deriveStagePipeline(baseInput({ status: "done" }));
    for (const s of pipeline.stages) {
      expect(s.state).toBe("passed");
    }
    expect(pipeline.current).toBe("review");
  });

  it("forces a stage that would otherwise be failed to passed", () => {
    const pipeline = deriveStagePipeline(baseInput({ status: "done", gateFailed: true }));
    expect(find(pipeline, "review").state).toBe("passed");
  });
});

describe("deriveStagePipeline — cancelled status", () => {
  it("derives states normally but never promotes pending to active", () => {
    const pipeline = deriveStagePipeline(baseInput({ status: "cancelled" }));
    expect(pipeline.current).toBe("dev");
    expect(find(pipeline, "dev")).toEqual({ stage: "dev", state: "pending" }); // NOT "active"
  });

  it("falls back to the last passed stage when everything is passed", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ status: "cancelled", prNumber: 1, prMerged: true }),
    );
    expect(pipeline.current).toBe("review");
    for (const s of pipeline.stages) {
      expect(s.state).toBe("passed");
    }
  });
});

describe("deriveStagePipeline — unknown status (enum drift)", () => {
  it("treats an unrecognized status string the same as 'todo' instead of crashing", () => {
    expect(() =>
      deriveStagePipeline(baseInput({ status: "some_future_status_v2" })),
    ).not.toThrow();

    const unknown = deriveStagePipeline(baseInput({ status: "some_future_status_v2" }));
    const todo = deriveStagePipeline(baseInput({ status: "todo" }));
    expect(unknown).toEqual(todo);
  });

  it("does not treat an unknown status as 'blocked'", () => {
    // dev is the earliest open stage here, so the normal current-promotion
    // applies and it reads "active", not "pending" — the important
    // assertion is that it never reads "blocked", which is the state an
    // unrecognized status could wrongly trigger if it fell through to a raw
    // string comparison.
    const pipeline = deriveStagePipeline(baseInput({ status: "weird-legacy-status" }));
    expect(find(pipeline, "dev").state).not.toBe("blocked");
    expect(find(pipeline, "dev").state).toBe("active");
  });
});
