import { describe, expect, it } from "vitest";
import { parseAutomationRunResponse } from "./schema";

describe("parseAutomationRunResponse", () => {
  it("fails closed when a newer or malformed retry response crosses the API boundary", () => {
    expect(parseAutomationRunResponse({ id: null, detail: null })).toMatchObject({
      id: "",
      status: "failed",
      actions_applied: 0,
      detail: {},
    });
  });

  it("preserves retry lineage and step errors", () => {
    const parsed = parseAutomationRunResponse({
      id: "run-2",
      automation_id: "automation-1",
      issue_id: "issue-1",
      trigger_type: "issue.label_attached",
      status: "applied",
      actions_applied: 1,
      detail: {
        retry_of: "run-1",
        actions: [{ type: "send_telegram", ok: true, detail: "notified" }],
      },
      error: "",
      created_at: "2026-08-20T00:00:00Z",
    });
    expect(parsed.detail.retry_of).toBe("run-1");
    expect(parsed.detail.actions?.[0]?.detail).toBe("notified");
  });
});
