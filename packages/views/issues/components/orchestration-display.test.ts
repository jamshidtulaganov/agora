import { describe, expect, it } from "vitest";
import type { AgentTask, OrchestrationEvent, OrchestrationRun, OrchestrationStep } from "@agora/core/types";
import {
  isManualOrchestrationBatchPaused,
  latestCompletedOrchestrationHandoff,
  orchestrationDisplayGroups,
  orchestrationHandoffText,
  orchestrationStepDisplayTitle,
} from "./orchestration-display";

function step(id: string, position: number, output?: unknown): OrchestrationStep {
  return {
    id,
    key: id,
    title: `Step ${id}`,
    stage: "dev",
    status: "completed",
    position,
    approval_required: false,
    attempt: 1,
    max_attempts: 1,
    instructions: "",
    output,
    depends_on_step_ids: [],
    merge_status: "clean",
    conflict_files: [],
    kind: "task",
    capability: "implementation",
    integration_status: "not_required",
    integrated_head_shas: [],
    missing_head_shas: [],
  };
}

describe("orchestration display handoffs", () => {
  it("extracts persisted output and removes progress protocol noise", () => {
    expect(orchestrationHandoffText({
      output: "PROGRESS: running checks\n```todo\n[]\n```\nImplemented the cache event.\n\nTests pass.",
    })).toBe("Implemented the cache event.\n\nTests pass.");
  });

  it("renders the summary from a structured handoff envelope", () => {
    expect(orchestrationHandoffText({
      schema_version: 1,
      stage: "dev",
      outcome: "completed",
      summary: "API contract updated; frontend can now refetch one run.",
      artifacts: ["events.go"],
    })).toBe("API contract updated; frontend can now refetch one run.");
  });

  it("does not expose the raw agora-handoff contract", () => {
    expect(orchestrationHandoffText({
      output: "Review done.\n```agora-handoff\n{\"schema_version\":1,\"summary\":\"Review done\",\"contracts\":[]}\n```",
    })).toBe("Review done.");
  });

  it("does not present empty output containers as a handoff", () => {
    expect(orchestrationHandoffText({})).toBe("");
    expect(orchestrationHandoffText([])).toBe("");
  });

  it("selects the latest completed step with an actual handoff", () => {
    const older = step("older", 1, { output: "Older handoff" });
    const newer = step("newer", 2, { output: "Newest handoff" });
    const titleOnly = step("title-only", 3);
    const events: OrchestrationEvent[] = [
      { id: "e1", step_id: older.id, kind: "step_completed", actor_type: "agent", details: {}, created_at: "2026-08-06T10:00:00Z" },
      { id: "e2", step_id: newer.id, kind: "step_completed", actor_type: "agent", details: {}, created_at: "2026-08-06T11:00:00Z" },
      { id: "e3", step_id: titleOnly.id, kind: "step_completed", actor_type: "agent", details: {}, created_at: "2026-08-06T12:00:00Z" },
    ];

    expect(latestCompletedOrchestrationHandoff([older, newer, titleOnly], events, [])).toEqual({
      step: newer,
      output: "Newest handoff",
    });
  });

  it("falls back to the completed task result when the step output is absent", () => {
    const completed = { ...step("task-backed", 1), task_id: "task-1" };
    const task = {
      id: "task-1",
      completed_at: "2026-08-06T12:00:00Z",
      result: { output: "Task result handoff" },
    } as AgentTask;

    expect(latestCompletedOrchestrationHandoff([completed], [], [task])?.output).toBe("Task result handoff");
  });
});

describe("orchestration stage presentation", () => {
  it("offers manual continuation only for a clean pause between persisted batches", () => {
    const paused = {
      progression_policy: "manual",
      status: "waiting_approval",
      steps: [step("done", 0), { ...step("next", 1), status: "pending" }],
    } as OrchestrationRun;

    expect(isManualOrchestrationBatchPaused(paused)).toBe(true);
    expect(isManualOrchestrationBatchPaused({
      ...paused,
      steps: [step("done", 0), { ...step("question", 1), status: "waiting_input" }],
    })).toBe(false);
    expect(isManualOrchestrationBatchPaused({
      ...paused,
      steps: [step("done", 0)],
    })).toBe(false);
  });

  it("groups only persisted steps and keeps parallel development workstreams distinct", () => {
    const steps = [
      { ...step("plan", 0), title: "Plan the work", stage: "plan" as const, capability: "coordination" as const },
      { ...step("api", 1), title: "Backend API", capability: "backend" as const },
      { ...step("web", 2), title: "Web application", capability: "frontend" as const },
      { ...step("integrate", 3), title: "Integrate implementation branches", kind: "integration" as const, capability: "integration" as const },
      { ...step("qa", 4), title: "Verify the integrated result", stage: "qa" as const, capability: "qa" as const },
      { ...step("review", 5), title: "Review the integrated result", stage: "review" as const, capability: "review" as const },
      { ...step("release", 6), title: "Approve and merge the change", stage: "release" as const, capability: "release" as const },
    ];

    expect(orchestrationDisplayGroups(steps).map((group) => ({
      kind: group.kind,
      steps: group.steps.map((item) => item.id),
    }))).toEqual([
      { kind: "plan", steps: ["plan"] },
      { kind: "dev", steps: ["api", "web"] },
      { kind: "integration", steps: ["integrate"] },
      { kind: "qa", steps: ["qa"] },
      { kind: "review", steps: ["review"] },
      { kind: "release", steps: ["release"] },
    ]);
  });

  it("omits stages that do not exist in the persisted plan", () => {
    const groups = orchestrationDisplayGroups([
      { ...step("implementation", 1), title: "Implement and verify the change" },
      { ...step("release", 2), title: "Merge the change", stage: "release", capability: "release" },
    ]);

    expect(groups.map((group) => group.kind)).toEqual(["dev", "release"]);
  });

  it("uses a specific persisted capability instead of a redundant generic title", () => {
    expect(orchestrationStepDisplayTitle({
      ...step("frontend", 1),
      title: "Development task",
      capability: "frontend",
    })).toBe("Frontend");
    expect(orchestrationStepDisplayTitle({
      ...step("api", 1),
      title: "Build the account API",
      capability: "backend",
    })).toBe("Build the account API");
  });
});
