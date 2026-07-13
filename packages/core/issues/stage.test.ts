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
  it("always returns the three stages in dev/qa/review order", () => {
    const pipeline = deriveStagePipeline(baseInput());
    expect(pipeline.stages.map((s) => s.stage)).toEqual(["dev", "qa", "review"]);
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

  it("sets an 'in editor' detail while pending in in_editor work mode", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ workMode: "in_editor", status: "cancelled" }),
    );
    expect(find(pipeline, "dev")).toMatchObject({ state: "pending", detail: "in editor" });
  });

  it("sets an 'in editor' detail while running in in_editor work mode", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ workMode: "in_editor", runningTaskStages: ["dev"] }),
    );
    expect(find(pipeline, "dev")).toMatchObject({ state: "running", detail: "in editor" });
  });

  it("does not set an 'in editor' detail once passed", () => {
    const pipeline = deriveStagePipeline(baseInput({ workMode: "in_editor", prNumber: 1 }));
    expect(find(pipeline, "dev")).toEqual({ stage: "dev", state: "passed" });
  });

  it("precedence: an opened PR wins over a blocked status", () => {
    const pipeline = deriveStagePipeline(baseInput({ prNumber: 7, status: "blocked" }));
    expect(find(pipeline, "dev").state).toBe("passed");
  });
});

describe("deriveStagePipeline — qa stage", () => {
  it("passes on a qa:pass label", () => {
    const pipeline = deriveStagePipeline(baseInput({ labels: [{ name: "qa:pass" }] }));
    expect(find(pipeline, "qa").state).toBe("passed");
  });

  it("passes when the issue is done, with no labels", () => {
    const pipeline = deriveStagePipeline(baseInput({ status: "done" }));
    expect(find(pipeline, "qa").state).toBe("passed");
  });

  it("fails on a qa:fail label", () => {
    const pipeline = deriveStagePipeline(baseInput({ labels: [{ name: "qa:fail" }] }));
    expect(find(pipeline, "qa").state).toBe("failed");
  });

  it("is blocked on a qa:blocked label", () => {
    const pipeline = deriveStagePipeline(baseInput({ labels: [{ name: "qa:blocked" }] }));
    expect(find(pipeline, "qa").state).toBe("blocked");
  });

  it("is running when a qa task is attributed as running", () => {
    const pipeline = deriveStagePipeline(baseInput({ runningTaskStages: ["qa"] }));
    expect(find(pipeline, "qa").state).toBe("running");
  });

  it("is active while in_review with no gating label", () => {
    const pipeline = deriveStagePipeline(baseInput({ status: "in_review" }));
    expect(find(pipeline, "qa").state).toBe("active");
  });

  it("is pending outside in_review with no label and nothing running", () => {
    const pipeline = deriveStagePipeline(baseInput());
    expect(find(pipeline, "qa").state).toBe("pending");
  });

  it("sets a 'stale' detail when qa:stale is present and the stage hasn't passed", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ status: "in_review", labels: [{ name: "qa:stale" }] }),
    );
    expect(find(pipeline, "qa")).toMatchObject({ state: "active", detail: "stale" });
  });

  it("suppresses the 'stale' detail once qa:pass has landed", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ labels: [{ name: "qa:pass" }, { name: "qa:stale" }] }),
    );
    expect(find(pipeline, "qa")).toEqual({ stage: "qa", state: "passed" });
  });

  it("precedence: status done wins over a qa:fail label", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ status: "done", labels: [{ name: "qa:fail" }] }),
    );
    expect(find(pipeline, "qa").state).toBe("passed");
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

  it("fails when the CI gate fails", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ mergeGates: { ci: "fail", qa: "pass", tier: "full" } }),
    );
    expect(find(pipeline, "review").state).toBe("failed");
  });

  it("fails when the QA gate fails", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ mergeGates: { ci: "pass", qa: "fail", tier: "full" } }),
    );
    expect(find(pipeline, "review").state).toBe("failed");
  });

  it("is active with no tier detail when a gate is pending and the qa stage has passed", () => {
    const pipeline = deriveStagePipeline(
      baseInput({
        labels: [{ name: "qa:pass" }],
        mergeGates: { ci: "pending", qa: "pass", tier: "light" },
      }),
    );
    // Tier is internal gate-policy jargon — never surfaced as a stepper detail.
    expect(find(pipeline, "review")).toEqual({ stage: "review", state: "active" });
  });

  it("stays pending (no tier detail) when a gate is pending but the qa stage hasn't passed yet", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ mergeGates: { ci: "pending", qa: "pending", tier: "light" } }),
    );
    expect(find(pipeline, "review")).toEqual({ stage: "review", state: "pending" });
  });

  it("is pending with no merge signals at all", () => {
    const pipeline = deriveStagePipeline(baseInput());
    expect(find(pipeline, "review")).toEqual({ stage: "review", state: "pending" });
  });

  it("never surfaces the gate tier as detail on a failed gate", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ mergeGates: { ci: "fail", qa: "pass", tier: "trivial" } }),
    );
    expect(find(pipeline, "review")).toEqual({ stage: "review", state: "failed" });
  });

  it("precedence: the override detail wins over the gate tier", () => {
    const pipeline = deriveStagePipeline(
      baseInput({
        labels: [{ name: "merge:override" }],
        mergeGates: { ci: "fail", qa: "fail", tier: "full" },
      }),
    );
    expect(find(pipeline, "review")).toMatchObject({ state: "passed", detail: "override" });
  });

  it("precedence: a merged PR wins over a failing gate", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ prMerged: true, mergeGates: { ci: "fail", qa: "fail", tier: "full" } }),
    );
    expect(find(pipeline, "review").state).toBe("passed");
  });
});

describe("deriveStagePipeline — review stage v2 (agent reviews, human approves)", () => {
  it("fails on a review:fail label", () => {
    const pipeline = deriveStagePipeline(baseInput({ labels: [{ name: "review:fail" }] }));
    expect(find(pipeline, "review").state).toBe("failed");
  });

  it("review:fail wins over green ci/qa merge gates", () => {
    const pipeline = deriveStagePipeline(
      baseInput({
        labels: [{ name: "review:fail" }],
        mergeGates: { ci: "pass", qa: "pass", tier: "full" },
      }),
    );
    expect(find(pipeline, "review")).toEqual({ stage: "review", state: "failed" });
  });

  it("is active 'awaiting approval' on review:pass once the qa stage has passed", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ labels: [{ name: "review:pass" }, { name: "qa:pass" }] }),
    );
    expect(find(pipeline, "review")).toMatchObject({
      state: "active",
      detail: "awaiting approval",
    });
  });

  it("does not go 'awaiting approval' on review:pass while qa hasn't passed", () => {
    const pipeline = deriveStagePipeline(baseInput({ labels: [{ name: "review:pass" }] }));
    expect(find(pipeline, "review").state).toBe("pending");
  });

  it("is active 'merging…' once merge:approved is stamped", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ labels: [{ name: "merge:approved" }, { name: "qa:pass" }] }),
    );
    expect(find(pipeline, "review")).toMatchObject({ state: "active", detail: "merging…" });
  });

  it("precedence: merge:approved wins over review:fail (the human decided)", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ labels: [{ name: "merge:approved" }, { name: "review:fail" }] }),
    );
    expect(find(pipeline, "review")).toMatchObject({ state: "active", detail: "merging…" });
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

  it("status done forces review passed even with review:fail present", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ status: "done", labels: [{ name: "review:fail" }] }),
    );
    expect(find(pipeline, "review").state).toBe("passed");
  });
});

describe("deriveStagePipeline — running attribution is per-stage", () => {
  it("only marks the stages the caller attributed as running", () => {
    // dev is stage index 0, so a non-cancelled status promotes its pending
    // base state to "active" (the current-stage promotion) — use cancelled
    // to observe that "qa" is the only stage marked "running".
    const pipeline = deriveStagePipeline(
      baseInput({ status: "cancelled", runningTaskStages: ["qa"] }),
    );
    expect(find(pipeline, "qa").state).toBe("running");
    expect(find(pipeline, "dev").state).toBe("pending"); // not attributed, stays pending
    expect(find(pipeline, "review").state).toBe("pending");
  });
});

describe("deriveStagePipeline — current stage resolution", () => {
  it("promotes only the current stage from pending to active", () => {
    const pipeline = deriveStagePipeline(baseInput());
    // dev is the first open (non-passed) stage.
    expect(pipeline.current).toBe("dev");
    expect(find(pipeline, "dev").state).toBe("active");
    // qa/review are also pending in this scenario but are NOT current, so
    // they must stay pending rather than also being promoted.
    expect(find(pipeline, "qa").state).toBe("pending");
    expect(find(pipeline, "review").state).toBe("pending");
  });

  it("does not promote a non-pending current stage (e.g. blocked)", () => {
    const pipeline = deriveStagePipeline(baseInput({ status: "blocked" }));
    expect(pipeline.current).toBe("dev");
    expect(find(pipeline, "dev").state).toBe("blocked");
  });

  it("does not re-promote a stage whose base state is already active", () => {
    // status in_review => dev auto-passes; qa lands on its own "active" via
    // the in_review branch (not via current-promotion).
    const pipeline = deriveStagePipeline(baseInput({ status: "in_review" }));
    expect(pipeline.current).toBe("qa");
    expect(find(pipeline, "qa").state).toBe("active");
  });

  it("advances current past every passed stage", () => {
    const pipeline = deriveStagePipeline(
      baseInput({
        prNumber: 1,
        labels: [{ name: "qa:pass" }],
      }),
    );
    // dev/qa passed -> review is first open.
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

  it("forces a stage that would otherwise be failed/blocked to passed", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ status: "done", labels: [{ name: "qa:fail" }] }),
    );
    expect(find(pipeline, "qa").state).toBe("passed");
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
      baseInput({
        status: "cancelled",
        prNumber: 1,
        labels: [{ name: "qa:pass" }],
        prMerged: true,
      }),
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
