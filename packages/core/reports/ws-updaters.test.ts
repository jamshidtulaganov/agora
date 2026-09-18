import { describe, it, expect, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import { onReportChanged } from "./ws-updaters";
import { reportKeys } from "./queries";

function queryClient() {
  const qc = new QueryClient();
  const invalidate = vi.spyOn(qc, "invalidateQueries");
  return { qc, invalidate };
}

describe("pinned report WS invalidation", () => {
  it("reconciles the project's report list and the open viewer", () => {
    const { qc, invalidate } = queryClient();
    onReportChanged(qc, "ws-1", { project_id: "proj-1", pin_id: "pin-1" });
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: reportKeys.project("ws-1", "proj-1"),
    });
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: reportKeys.detail("ws-1", "pin-1"),
    });
  });

  it("falls back to every report in the workspace when the payload has no project", () => {
    const { qc, invalidate } = queryClient();
    onReportChanged(qc, "ws-1", { project_id: "", pin_id: "pin-1" });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: reportKeys.all("ws-1") });
  });

  it("handles the 2b schedule event's extra artifact_id without special-casing it", () => {
    // `report:schedule_changed` carries one more field than the 2a events and
    // shares this updater — the cadence badge lives on the same list query.
    const { qc, invalidate } = queryClient();
    onReportChanged(qc, "ws-1", {
      project_id: "proj-1",
      pin_id: "pin-1",
      artifact_id: "art-1",
    });
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: reportKeys.project("ws-1", "proj-1"),
    });
  });

  it("does nothing without a workspace (no wsId, no key to invalidate)", () => {
    const { qc, invalidate } = queryClient();
    onReportChanged(qc, "", { project_id: "proj-1", pin_id: "pin-1" });
    expect(invalidate).not.toHaveBeenCalled();
  });
});
