import { describe, expect, it } from "vitest";
import {
  MAX_FOLLOW_UPS,
  followUpsForTools,
  followUpsForTranscript,
  toolNamesInFinalTurn,
} from "./follow-ups";

const row = (role: string, tool_name?: string) => ({ role, tool_name });

describe("toolNamesInFinalTurn", () => {
  it("only reads the tools called after the last user turn", () => {
    const messages = [
      row("user"),
      row("tool", "search_issues"),
      row("assistant"),
      row("user"),
      row("tool", "create_issue"),
      row("assistant"),
    ];

    expect(toolNamesInFinalTurn(messages)).toEqual(["create_issue"]);
  });

  it("returns nothing for a transcript with no tool rows", () => {
    expect(toolNamesInFinalTurn([row("user"), row("assistant")])).toEqual([]);
  });
});

describe("followUpsForTools", () => {
  it("suggests follow-ups after a single mutating tool", () => {
    expect(followUpsForTools(["create_issue"])).toEqual([
      "assign_issue",
      "set_due_date",
      "add_issue_context",
    ]);
  });

  it("suggests follow-ups after a single analytics tool", () => {
    expect(followUpsForTools(["usage_summary"])).toEqual(["compare_last_week", "usage_by_agent"]);
  });

  it("stays silent when several recognized tools ran — 'it' would be ambiguous", () => {
    expect(followUpsForTools(["create_issue", "usage_summary"])).toEqual([]);
    expect(followUpsForTools(["create_issue", "create_issue"])).toEqual([]);
  });

  it("stays silent for an unrecognized or empty turn", () => {
    expect(followUpsForTools([])).toEqual([]);
    expect(followUpsForTools(["some_future_tool"])).toEqual([]);
  });

  it("ignores unrecognized companions of the one tool it knows", () => {
    expect(followUpsForTools(["list_workspaces", "usage_summary"])).toEqual([
      "compare_last_week",
      "usage_by_agent",
    ]);
  });

  it("never returns more than the cap", () => {
    expect(followUpsForTools(["create_issue"]).length).toBeLessThanOrEqual(MAX_FOLLOW_UPS);
  });
});

describe("followUpsForTranscript", () => {
  it("wires the two halves together", () => {
    expect(
      followUpsForTranscript([row("user"), row("tool", "list_my_issues"), row("assistant")]),
    ).toEqual(["what_is_urgent"]);
  });
});
