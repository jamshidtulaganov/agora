import { describe, it, expect } from "vitest";
import type { AssistantMessage } from "@agora/core/types";
import {
  operationOutcomeAfter,
  parseConfirmationRequest,
  parseOperationTarget,
  parsePlanItemResults,
  parsePlanItems,
  parseReceipt,
  parseUncertainOutcome,
  planItemResultsAfter,
  targetLabel,
} from "./operation";

// These decoders sit on the drift boundary: `tool_result` is opaque JSON that
// an older server never produces and a newer one may reshape. Every "returns
// null" case below is a transcript row that falls back to the plain ToolChip
// instead of crashing or rendering an empty card.

const PENDING = {
  status: "needs_confirmation",
  operation: {
    id: "op-1",
    tool_name: "delete_issue",
    summary: "Delete MUL-123",
    workspace_slug: "acme",
    target: { type: "issue", identifier: "MUL-123", title: "Fix the login redirect" },
  },
};

function toolRow(id: string, result: unknown): AssistantMessage {
  return {
    id,
    session_id: "s1",
    role: "tool",
    content: "{}",
    tool_name: "delete_issue",
    tool_result: result,
    created_at: "2026-09-17T10:00:00Z",
  };
}

describe("parseConfirmationRequest", () => {
  it("reads the pinned needs_confirmation shape", () => {
    expect(parseConfirmationRequest(PENDING)).toEqual({
      operationId: "op-1",
      toolName: "delete_issue",
      summary: "Delete MUL-123",
      workspaceSlug: "acme",
      target: { type: "issue", identifier: "MUL-123", title: "Fix the login redirect" },
      kind: "",
      items: [],
    });
  });

  it("keeps the card when only the operation id is present", () => {
    // Summary/slug/target are cosmetic — the binding is the id.
    expect(parseConfirmationRequest({ status: "needs_confirmation", operation: { id: "op-2" } }))
      .toEqual({
        operationId: "op-2",
        toolName: "",
        summary: "",
        workspaceSlug: "",
        target: null,
        kind: "",
        items: [],
      });
  });

  it("returns null without an operation id — a button with nothing to bind to", () => {
    expect(
      parseConfirmationRequest({ status: "needs_confirmation", operation: { summary: "Delete it" } }),
    ).toBeNull();
  });

  it("returns null for every other result shape", () => {
    expect(parseConfirmationRequest(null)).toBeNull();
    expect(parseConfirmationRequest("needs_confirmation")).toBeNull();
    expect(parseConfirmationRequest([PENDING])).toBeNull();
    expect(parseConfirmationRequest({ status: "ok", operation: { id: "op-1" } })).toBeNull();
    expect(parseConfirmationRequest({ status: "needs_confirmation" })).toBeNull();
    expect(parseConfirmationRequest({ status: "needs_confirmation", operation: "op-1" })).toBeNull();
    expect(parseConfirmationRequest({ error: "not allowed" })).toBeNull();
  });
});

describe("parseOperationTarget", () => {
  it("accepts a bare string as a label", () => {
    expect(parseOperationTarget("Acme billing")).toEqual({
      type: "",
      identifier: "",
      title: "Acme billing",
    });
  });

  it("accepts the field aliases an older/newer server may use", () => {
    expect(parseOperationTarget({ kind: "project", key: "PRJ-9", name: "Billing" })).toEqual({
      type: "project",
      identifier: "PRJ-9",
      title: "Billing",
    });
  });

  it("returns null when nothing identifies the target", () => {
    expect(parseOperationTarget({ type: "issue" })).toBeNull();
    expect(parseOperationTarget(null)).toBeNull();
    expect(parseOperationTarget(42)).toBeNull();
  });

  it("joins identifier and title into one line", () => {
    expect(targetLabel({ type: "issue", identifier: "MUL-1", title: "Fix" })).toBe("MUL-1 · Fix");
    expect(targetLabel({ type: "issue", identifier: "", title: "Fix" })).toBe("Fix");
    expect(targetLabel(null)).toBe("");
  });
});

describe("parseReceipt", () => {
  it("reads the pinned {action, target, links, effects} shape", () => {
    expect(
      parseReceipt({
        receipt: {
          action: "Assigned issue",
          target: { type: "issue", identifier: "MUL-123", title: "Fix the login redirect" },
          links: [{ label: "MUL-123", url: "/acme/issues/MUL-123" }],
          effects: ["Notified 2 subscribers", "Moved to In Progress"],
        },
      }),
    ).toEqual({
      action: "Assigned issue",
      target: { type: "issue", identifier: "MUL-123", title: "Fix the login redirect" },
      links: [{ label: "MUL-123", href: "/acme/issues/MUL-123" }],
      effects: ["Notified 2 subscribers", "Moved to In Progress"],
    });
  });

  it("accepts the server's bare-string url_path links and labels them by identifier", () => {
    const receipt = parseReceipt({
      receipt: { action: "create_issue", links: ["/acme/issues/MUL-9"], effects: ["Added to Sprint 4"] },
    });
    expect(receipt?.links).toEqual([{ label: "MUL-9", href: "/acme/issues/MUL-9" }]);
    expect(receipt?.effects).toEqual(["Added to Sprint 4"]);
  });

  // Exactly what server/internal/handler/assistant_operations.go attaches.
  it("reads the receipt the Go executor actually attaches", () => {
    expect(
      parseReceipt({
        issue: { identifier: "MUL-9", title: "Fix the login redirect" },
        receipt: {
          action: "archive_issue",
          target: { type: "issue", identifier: "MUL-9", title: "Fix the login redirect" },
          links: ["/acme/issues/MUL-9"],
          effects: ["moved to the archive"],
        },
      }),
    ).toEqual({
      action: "archive_issue",
      target: { type: "issue", identifier: "MUL-9", title: "Fix the login redirect" },
      links: [{ label: "MUL-9", href: "/acme/issues/MUL-9" }],
      effects: ["moved to the archive"],
    });
  });

  it("drops off-site links — AppLink routes in-app and would push a garbage route", () => {
    const receipt = parseReceipt({
      receipt: {
        action: "Created issue",
        links: [
          { label: "GitHub", url: "https://github.com/agora/agora/pull/1" },
          { label: "Issue", url: "/acme/issues/MUL-9" },
          "not-a-path",
          42,
        ],
      },
    });
    expect(receipt?.links).toEqual([{ label: "Issue", href: "/acme/issues/MUL-9" }]);
  });

  it("survives links/effects that are not arrays at all", () => {
    const receipt = parseReceipt({ receipt: { action: "Closed issue", links: null, effects: "none" } });
    expect(receipt).toEqual({ action: "Closed issue", target: null, links: [], effects: [] });
  });

  it("returns null without an action — a blank headline is worse than the plain chip", () => {
    expect(parseReceipt({ receipt: { target: { identifier: "MUL-1" } } })).toBeNull();
    expect(parseReceipt({ receipt: {} })).toBeNull();
  });

  it("returns null for results that carry no receipt", () => {
    expect(parseReceipt({ id: "issue-1", url: "/acme/issues/MUL-1" })).toBeNull();
    expect(parseReceipt(null)).toBeNull();
    expect(parseReceipt("done")).toBeNull();
    expect(parseReceipt({ receipt: "Assigned issue" })).toBeNull();
  });

  it("never upgrades a pending confirmation into a receipt", () => {
    expect(parseReceipt({ ...PENDING, receipt: { action: "Deleted issue" } })).toBeNull();
  });
});

describe("parseUncertainOutcome", () => {
  it("reads {status: uncertain, inspect}", () => {
    expect(parseUncertainOutcome({ status: "uncertain", inspect: "Check MUL-123 before retrying." }))
      .toEqual({ inspect: "Check MUL-123 before retrying." });
  });

  it("keeps the chip when the hint is missing", () => {
    expect(parseUncertainOutcome({ status: "uncertain" })).toEqual({ inspect: "" });
  });

  it("returns null for any other status", () => {
    expect(parseUncertainOutcome({ status: "ok" })).toBeNull();
    expect(parseUncertainOutcome({ inspect: "look here" })).toBeNull();
    expect(parseUncertainOutcome(undefined)).toBeNull();
  });
});

describe("operationOutcomeAfter", () => {
  it("returns null while nothing later references the operation", () => {
    const messages = [toolRow("m1", PENDING), toolRow("m2", { receipt: { action: "Something else" } })];
    expect(operationOutcomeAfter(messages, 0, "op-1")).toBeNull();
  });

  it("matches the server's synthetic op_<id> receipt tool_call_id", () => {
    // The confirmed execution's receipt row carries no operation id in its
    // body — the link back to the card is the tool_call_id the server mints
    // (assistantReceiptTag). Without this the card would stay pending forever
    // after a reload.
    const receipt: AssistantMessage = {
      ...toolRow("m2", { issue: { identifier: "MUL-123" }, receipt: { action: "delete_issue" } }),
      tool_call_id: "op_op-1",
    };
    expect(operationOutcomeAfter([toolRow("m1", PENDING), receipt], 0, "op-1")).toBe("confirmed");
  });

  it("reads the server's cancelled row as rejected", () => {
    const cancelled: AssistantMessage = {
      ...toolRow("m2", {
        status: "cancelled",
        cancelled: true,
        operation: { id: "op-1", tool_name: "delete_issue" },
        note: "The user cancelled this action.",
      }),
      tool_call_id: "op_op-1",
    };
    expect(operationOutcomeAfter([toolRow("m1", PENDING), cancelled], 0, "op-1")).toBe("rejected");
  });

  it("reads a later receipt as confirmed", () => {
    const messages = [
      toolRow("m1", PENDING),
      toolRow("m2", { operation_id: "op-1", receipt: { action: "Deleted issue" } }),
    ];
    expect(operationOutcomeAfter(messages, 0, "op-1")).toBe("confirmed");
  });

  it("reads the nested id shapes a receipt may carry", () => {
    expect(
      operationOutcomeAfter([toolRow("m1", PENDING), toolRow("m2", { operation: { id: "op-1" } })], 0, "op-1"),
    ).toBe("processing");
    expect(
      operationOutcomeAfter(
        [toolRow("m1", PENDING), toolRow("m2", { receipt: { operation_id: "op-1", action: "Deleted" } })],
        0,
        "op-1",
      ),
    ).toBe("confirmed");
  });

  it("reads a cancelled row as rejected and a stale one as expired", () => {
    expect(
      operationOutcomeAfter([toolRow("m1", PENDING), toolRow("m2", { operation_id: "op-1", status: "cancelled" })], 0, "op-1"),
    ).toBe("rejected");
    expect(
      operationOutcomeAfter([toolRow("m1", PENDING), toolRow("m2", { operation_id: "op-1", status: "expired" })], 0, "op-1"),
    ).toBe("expired");
  });

  it("ignores a repeat of the same pending row — that is the thing being waited on", () => {
    const messages = [toolRow("m1", PENDING), toolRow("m2", PENDING)];
    expect(operationOutcomeAfter(messages, 0, "op-1")).toBeNull();
  });

  it("ignores rows BEFORE the card and non-tool rows", () => {
    const messages: AssistantMessage[] = [
      toolRow("m0", { operation_id: "op-1", status: "cancelled" }),
      toolRow("m1", PENDING),
      { id: "m2", session_id: "s1", role: "assistant", content: "done", created_at: "" },
    ];
    expect(operationOutcomeAfter(messages, 1, "op-1")).toBeNull();
  });

  it("returns null for an empty operation id rather than matching everything", () => {
    expect(operationOutcomeAfter([toolRow("m1", { operation_id: "" })], -1, "")).toBeNull();
  });
});

// --- plans --------------------------------------------------------------
// A plan is the same needs_confirmation payload with `kind:"plan"` and rows
// (docs/assistant-domain-plan.md, "3a wire contract"). Everything below is a
// shape some deployed server can send: one that predates plans (no kind, no
// items), one mid-execution, one that renamed a field.

const PLAN = {
  status: "needs_confirmation",
  operation: {
    id: "op-2",
    kind: "plan",
    summary: "Plan sprint 12",
    workspace_slug: "acme",
    expires_at: "2026-09-19T18:00:00Z",
    items: [
      { index: 0, tool: "create_sprint", summary: "Create sprint 12" },
      { index: 1, tool: "move_issue_to_sprint", summary: "Move MUL-1 into sprint 12" },
    ],
  },
};

describe("parseConfirmationRequest — plan", () => {
  it("decodes the kind and the rows", () => {
    const request = parseConfirmationRequest(PLAN);
    expect(request?.kind).toBe("plan");
    expect(request?.items).toEqual([
      { index: 0, tool: "create_sprint", summary: "Create sprint 12" },
      { index: 1, tool: "move_issue_to_sprint", summary: "Move MUL-1 into sprint 12" },
    ]);
  });

  it("reports an ABSENT kind as a single operation — an older runtime's shape", () => {
    const request = parseConfirmationRequest(PENDING);
    expect(request?.kind).toBe("");
    expect(request?.items).toEqual([]);
  });
});

describe("parsePlanItems", () => {
  it("returns [] for a malformed list so the card falls back to the single-op rendering", () => {
    expect(parsePlanItems(null)).toEqual([]);
    expect(parsePlanItems({ items: [] })).toEqual([]);
    expect(parsePlanItems("create_issue")).toEqual([]);
    expect(parsePlanItems([1, "x", null])).toEqual([]);
  });

  it("falls back to the array position when a row lost its index", () => {
    expect(parsePlanItems([{ tool: "create_issue", summary: "Add the issue" }])).toEqual([
      { index: 0, tool: "create_issue", summary: "Add the issue" },
    ]);
  });

  it("drops a row with nothing to show and keeps the rest at their server index", () => {
    expect(parsePlanItems([{ index: 0 }, { index: 1, summary: "Move MUL-1" }])).toEqual([
      { index: 1, tool: "", summary: "Move MUL-1" },
    ]);
  });
});

describe("parsePlanItemResults", () => {
  it("decodes the four known outcomes", () => {
    expect(
      parsePlanItemResults([
        { index: 0, outcome: "ok", identifier: "MUL-9" },
        { index: 1, outcome: "failed", error: "Sprint is closed" },
        { index: 2, outcome: "skipped" },
        { index: 3, outcome: "not_run" },
      ]),
    ).toEqual([
      { index: 0, outcome: "ok", identifier: "MUL-9", error: "" },
      { index: 1, outcome: "failed", identifier: "", error: "Sprint is closed" },
      { index: 2, outcome: "skipped", identifier: "", error: "" },
      { index: 3, outcome: "not_run", identifier: "", error: "" },
    ]);
  });

  it("downgrades an outcome this build doesn't know instead of crashing", () => {
    expect(parsePlanItemResults([{ index: 0, outcome: "deferred_to_agent" }])).toEqual([
      { index: 0, outcome: "unknown", identifier: "", error: "" },
    ]);
  });

  it("skips proposal rows — an item with no outcome is not a result", () => {
    expect(parsePlanItemResults([{ index: 0, tool: "create_issue", summary: "Add it" }])).toEqual([]);
  });

  it("returns [] for null / non-list items", () => {
    expect(parsePlanItemResults(null)).toEqual([]);
    expect(parsePlanItemResults({ 0: { outcome: "ok" } })).toEqual([]);
  });
});

describe("planItemResultsAfter", () => {
  it("reads the per-row outcomes off the persisted receipt row", () => {
    const messages = [
      toolRow("m1", PLAN),
      toolRow("m2", {
        operation_id: "op-2",
        receipt: {
          action: "propose_plan",
          items: [
            { index: 0, outcome: "ok", identifier: "Sprint 12" },
            { index: 1, outcome: "failed", error: "Issue archived" },
          ],
        },
      }),
    ];
    expect(planItemResultsAfter(messages, 0, "op-2")).toEqual([
      { index: 0, outcome: "ok", identifier: "Sprint 12", error: "" },
      { index: 1, outcome: "failed", identifier: "", error: "Issue archived" },
    ]);
    // The overall state still comes from the same row.
    expect(operationOutcomeAfter(messages, 0, "op-2")).toBe("confirmed");
  });

  it("reads a top-level items array too", () => {
    const messages = [
      toolRow("m1", PLAN),
      toolRow("m2", { operation_id: "op-2", status: "confirmed", items: [{ index: 0, outcome: "ok" }] }),
    ];
    expect(planItemResultsAfter(messages, 0, "op-2")).toEqual([
      { index: 0, outcome: "ok", identifier: "", error: "" },
    ]);
  });

  it("returns [] while the plan is still pending, and for a receipt without items", () => {
    expect(planItemResultsAfter([toolRow("m1", PLAN)], 0, "op-2")).toEqual([]);
    expect(
      planItemResultsAfter(
        [toolRow("m1", PLAN), toolRow("m2", { operation_id: "op-2", receipt: { action: "propose_plan" } })],
        0,
        "op-2",
      ),
    ).toEqual([]);
  });
});
