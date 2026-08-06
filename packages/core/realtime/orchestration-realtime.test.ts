import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { issueKeys } from "../issues/queries";
import { invalidateOrchestrationChanged } from "./use-realtime-sync";

describe("orchestration:changed realtime invalidation", () => {
  it("invalidates only the named issue orchestration cache", () => {
    const qc = new QueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");

    invalidateOrchestrationChanged(qc, {
      issue_id: "issue-42",
      run_id: "run-7",
      step_id: "step-3",
      kind: "step_completed",
      plan_version: 4,
    });

    expect(invalidate).toHaveBeenCalledOnce();
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: issueKeys.orchestration("issue-42"),
      exact: true,
    });
  });

  it("ignores malformed frames without issue/run identity", () => {
    const qc = new QueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");

    invalidateOrchestrationChanged(qc, {
      issue_id: "",
      run_id: "",
      kind: "step_completed",
    });

    expect(invalidate).not.toHaveBeenCalled();
  });
});
