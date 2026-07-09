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
    hasDesignSignals: false,
    hasDeployTarget: false,
    ...overrides,
  };
}

function find(pipeline: StagePipeline, stage: SDLCStage) {
  const found = pipeline.stages.find((s) => s.stage === stage);
  if (!found) throw new Error(`stage ${stage} missing from pipeline`);
  return found;
}

describe("deriveStagePipeline — stage order", () => {
  it("always returns the five stages in design/dev/qa/review/deploy order", () => {
    const pipeline = deriveStagePipeline(baseInput());
    expect(pipeline.stages.map((s) => s.stage)).toEqual([
      "design",
      "dev",
      "qa",
      "review",
      "deploy",
    ]);
  });
});

describe("deriveStagePipeline — design stage", () => {
  it("is skipped when the issue has no design signals", () => {
    const pipeline = deriveStagePipeline(baseInput({ hasDesignSignals: false }));
    expect(find(pipeline, "design")).toEqual({ stage: "design", state: "skipped" });
  });

  it("passes on a design verdict of pass", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ hasDesignSignals: true, designVerdict: "pass" }),
    );
    expect(find(pipeline, "design").state).toBe("passed");
  });

  it("passes via the QA-verdict override (mirrors backend design_action.go)", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ hasDesignSignals: true, designVerdict: null, qaVerdict: "pass" }),
    );
    expect(find(pipeline, "design").state).toBe("passed");
  });

  it("passes when the issue is done, regardless of verdicts", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ hasDesignSignals: true, status: "done" }),
    );
    expect(find(pipeline, "design").state).toBe("passed");
  });

  it("fails on a design verdict of fail", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ hasDesignSignals: true, designVerdict: "fail" }),
    );
    expect(find(pipeline, "design").state).toBe("failed");
  });

  it("is running when a design task is attributed as running", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ hasDesignSignals: true, runningTaskStages: ["design"] }),
    );
    expect(find(pipeline, "design").state).toBe("running");
  });

  it("is pending with signals but no verdict and nothing running", () => {
    // design is always stage index 0, so whenever it's open it's also the
    // "current" stage and a pending base state gets promoted to "active"
    // (see the current-resolution describe block). Use cancelled status,
    // which never promotes, to observe the raw base state here.
    const pipeline = deriveStagePipeline(
      baseInput({ hasDesignSignals: true, status: "cancelled" }),
    );
    expect(find(pipeline, "design").state).toBe("pending");
  });

  it("precedence: status done wins over a fail verdict", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ hasDesignSignals: true, designVerdict: "fail", status: "done" }),
    );
    expect(find(pipeline, "design").state).toBe("passed");
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
    // design is skipped by default, making dev the earliest open stage, so a
    // non-cancelled status would promote it to "active". Use cancelled to
    // observe the raw base state.
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

  it("is active when a gate is pending and the qa stage has passed", () => {
    const pipeline = deriveStagePipeline(
      baseInput({
        labels: [{ name: "qa:pass" }],
        mergeGates: { ci: "pending", qa: "pass", tier: "light" },
      }),
    );
    expect(find(pipeline, "review")).toMatchObject({ state: "active", detail: "light" });
  });

  it("stays pending when a gate is pending but the qa stage hasn't passed yet", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ mergeGates: { ci: "pending", qa: "pending", tier: "light" } }),
    );
    expect(find(pipeline, "review")).toMatchObject({ state: "pending", detail: "light" });
  });

  it("is pending with no merge signals at all", () => {
    const pipeline = deriveStagePipeline(baseInput());
    expect(find(pipeline, "review")).toEqual({ stage: "review", state: "pending" });
  });

  it("passes the gate tier through as detail on a failed gate", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ mergeGates: { ci: "fail", qa: "pass", tier: "trivial" } }),
    );
    expect(find(pipeline, "review")).toMatchObject({ state: "failed", detail: "trivial" });
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

describe("deriveStagePipeline — deploy stage", () => {
  it("is skipped when the project has no deploy target", () => {
    const pipeline = deriveStagePipeline(baseInput({ hasDeployTarget: false }));
    expect(find(pipeline, "deploy")).toEqual({ stage: "deploy", state: "skipped" });
  });

  it("passes once synced", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ hasDeployTarget: true, deploySynced: true }),
    );
    expect(find(pipeline, "deploy").state).toBe("passed");
  });

  it("is running when a deploy task is attributed as running", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ hasDeployTarget: true, runningTaskStages: ["deploy"] }),
    );
    expect(find(pipeline, "deploy").state).toBe("running");
  });

  it("is pending with a target that isn't synced or running", () => {
    const pipeline = deriveStagePipeline(baseInput({ hasDeployTarget: true }));
    expect(find(pipeline, "deploy").state).toBe("pending");
  });
});

describe("deriveStagePipeline — running attribution is per-stage", () => {
  it("only marks the stages the caller attributed as running", () => {
    const pipeline = deriveStagePipeline(
      baseInput({
        hasDesignSignals: true,
        hasDeployTarget: true,
        runningTaskStages: ["design", "deploy"],
      }),
    );
    expect(find(pipeline, "design").state).toBe("running");
    expect(find(pipeline, "dev").state).toBe("pending"); // not attributed, stays pending
    expect(find(pipeline, "qa").state).toBe("pending");
    expect(find(pipeline, "deploy").state).toBe("running");
  });
});

describe("deriveStagePipeline — current stage resolution", () => {
  it("promotes only the current stage from pending to active", () => {
    const pipeline = deriveStagePipeline(baseInput({ hasDesignSignals: false }));
    // design skipped -> dev is the first open (non-passed/skipped) stage.
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

  it("advances current past every passed/skipped stage", () => {
    const pipeline = deriveStagePipeline(
      baseInput({
        hasDesignSignals: true,
        designVerdict: "pass",
        prNumber: 1,
        labels: [{ name: "qa:pass" }],
        hasDeployTarget: false, // deploy skipped
      }),
    );
    // design/dev/qa passed, deploy skipped -> review is first open.
    expect(pipeline.current).toBe("review");
    expect(find(pipeline, "review").state).toBe("active"); // promoted from pending
  });
});

describe("deriveStagePipeline — done status", () => {
  it("forces every non-skipped stage to passed and pins current to the last non-skipped stage", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ status: "done", hasDesignSignals: true, hasDeployTarget: true }),
    );
    for (const s of pipeline.stages) {
      expect(s.state).toBe("passed");
    }
    expect(pipeline.current).toBe("deploy");
  });

  it("skips a stage with no signals even when done, and pins current to the last non-skipped stage", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ status: "done", hasDesignSignals: false, hasDeployTarget: false }),
    );
    expect(find(pipeline, "design").state).toBe("skipped");
    expect(find(pipeline, "deploy").state).toBe("skipped");
    expect(find(pipeline, "dev").state).toBe("passed");
    expect(find(pipeline, "qa").state).toBe("passed");
    expect(find(pipeline, "review").state).toBe("passed");
    expect(pipeline.current).toBe("review"); // last non-skipped
  });

  it("forces a stage that would otherwise be failed/blocked to passed", () => {
    const pipeline = deriveStagePipeline(
      baseInput({
        status: "done",
        hasDesignSignals: true,
        designVerdict: "fail",
        hasDeployTarget: true,
      }),
    );
    expect(find(pipeline, "design").state).toBe("passed");
    expect(find(pipeline, "deploy").state).toBe("passed");
  });
});

describe("deriveStagePipeline — cancelled status", () => {
  it("derives states normally but never promotes pending to active", () => {
    const pipeline = deriveStagePipeline(
      baseInput({ status: "cancelled", hasDesignSignals: true }),
    );
    expect(pipeline.current).toBe("design");
    expect(find(pipeline, "design")).toEqual({ stage: "design", state: "pending" }); // NOT "active"
  });

  it("falls back to the last non-skipped stage when everything is passed/skipped", () => {
    const pipeline = deriveStagePipeline(
      baseInput({
        status: "cancelled",
        hasDesignSignals: false,
        prNumber: 1,
        labels: [{ name: "qa:pass" }],
        prMerged: true,
        hasDeployTarget: false,
      }),
    );
    expect(pipeline.current).toBe("review");
    for (const s of pipeline.stages) {
      expect(["passed", "skipped"]).toContain(s.state);
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
    // dev is the earliest open stage here (design skipped by default), so
    // the normal current-promotion applies and it reads "active", not
    // "pending" — the important assertion is that it never reads "blocked",
    // which is the state an unrecognized status could wrongly trigger if it
    // fell through to a raw string comparison.
    const pipeline = deriveStagePipeline(baseInput({ status: "weird-legacy-status" }));
    expect(find(pipeline, "dev").state).not.toBe("blocked");
    expect(find(pipeline, "dev").state).toBe("active");
  });
});
