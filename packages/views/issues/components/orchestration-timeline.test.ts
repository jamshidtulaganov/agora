import { describe, expect, it } from "vitest";
import type { OrchestrationStep } from "@agora/core/types";
import { arrangeOrchestrationSteps } from "./orchestration-timeline";

function step(id: string, position: number, parentStepID?: string): OrchestrationStep {
  return {
    id,
    key: id,
    title: id,
    stage: "dev",
    status: "completed",
    position,
    approval_required: false,
    attempt: 1,
    max_attempts: 2,
    instructions: "",
    depends_on_step_ids: [],
    parent_step_id: parentStepID,
    merge_status: "clean",
    conflict_files: [],
    kind: id === "integration" ? "integration" : "task",
    capability: id === "integration" ? "integration" : "implementation",
    integration_status: id === "integration" ? "complete" : "not_required",
    integrated_head_shas: [],
    missing_head_shas: [],
  };
}

describe("arrangeOrchestrationSteps", () => {
  it("keeps late-added squad children with their parent before release", () => {
    const arranged = arrangeOrchestrationSteps([
      step("dev", 0),
      step("release", 1),
      step("review-branch", 2, "dev"),
      step("integration", 3, "dev"),
    ]);

    expect(arranged.map(({ step: item }) => item.id)).toEqual(["dev", "review-branch", "integration", "release"]);
    expect(arranged.map(({ depth }) => depth)).toEqual([0, 1, 1, 0]);
  });
});
