import { describe, expect, it } from "vitest";
import {
  conditionValueToText,
  humanizeMachineName,
  labelFor,
  operatorTakesValue,
  stepConfigFields,
  summarizeFlow,
  textToConditionValue,
} from "./flow-labels";

describe("labelFor", () => {
  it("uses the translation when there is one", () => {
    expect(labelFor({ "issue.created": "an issue is created" }, "issue.created")).toBe("an issue is created");
  });

  // The server owns the vocabulary: a node type this client has never seen must
  // still read as something, not as a blank chip.
  it("downgrades an unknown key to readable text", () => {
    expect(labelFor({}, "tracker.stage_changed")).toBe("tracker stage changed");
    expect(labelFor(undefined, "send_carrier_pigeon")).toBe("send carrier pigeon");
  });

  it("treats an empty translation as missing", () => {
    expect(labelFor({ "issue.created": "   " }, "issue.created")).toBe("issue created");
  });

  it("returns empty for an empty key", () => {
    expect(labelFor({ a: "b" }, "  ")).toBe("");
  });
});

describe("condition value round-trip", () => {
  it("edits a single value as plain text", () => {
    expect(conditionValueToText("review:fail")).toBe("review:fail");
    expect(textToConditionValue("review:fail")).toBe("review:fail");
  });

  it("edits a list as comma-separated text", () => {
    expect(conditionValueToText(["a", "b"])).toBe("a, b");
    expect(textToConditionValue("a, b")).toEqual(["a", "b"]);
  });

  it("drops blanks and stray commas", () => {
    expect(textToConditionValue("a, , b,")).toEqual(["a", "b"]);
    expect(textToConditionValue("  ")).toBe("");
  });

  it("handles a missing value", () => {
    expect(conditionValueToText(undefined)).toBe("");
  });
});

describe("operatorTakesValue", () => {
  it("hides the value box only for exists", () => {
    expect(operatorTakesValue("exists")).toBe(false);
    expect(operatorTakesValue("eq")).toBe(true);
    expect(operatorTakesValue("has_label")).toBe(true);
  });
});

describe("summarizeFlow", () => {
  const stepLabels = { set_status: "Set the status", send_telegram: "Send a Telegram message" };

  it("joins the trigger and its steps", () => {
    expect(summarizeFlow("a label is attached", [{ type: "set_status" }, { type: "send_telegram" }], stepLabels))
      .toBe("a label is attached → Set the status, Send a Telegram message");
  });

  it("falls back to the trigger alone when there are no steps", () => {
    expect(summarizeFlow("a label is attached", [], stepLabels)).toBe("a label is attached");
  });

  it("still names an unknown step type", () => {
    expect(summarizeFlow("when", [{ type: "do_magic" }], stepLabels)).toBe("when → do magic");
  });
});

describe("stepConfigFields", () => {
  it("returns the fields each step edits", () => {
    expect(stepConfigFields("set_status")).toEqual(["status"]);
    expect(stepConfigFields("send_telegram")).toEqual(["destination", "text", "chat_id"]);
  });

  // An unknown step must round-trip untouched: showing no fields is how its config
  // survives a save from an older client.
  it("shows no fields for an unknown step", () => {
    expect(stepConfigFields("do_magic")).toEqual([]);
  });
});

describe("humanizeMachineName", () => {
  it("replaces separators", () => {
    expect(humanizeMachineName("issue.label_attached")).toBe("issue label attached");
  });
});
