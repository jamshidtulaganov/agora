import { describe, it, expect } from "vitest";
import {
  deriveStepRunVerdict,
  serializeStepResults,
  parseStepResults,
  type StepResult,
} from "./step-run";

describe("deriveStepRunVerdict", () => {
  it("fails the case on any failing step", () => {
    expect(
      deriveStepRunVerdict([
        { step: 1, status: "pass" },
        { step: 2, status: "fail" },
        { step: 3, status: "skip" },
      ]),
    ).toBe("fail");
  });

  it("passes when every step passed (skips allowed alongside passes)", () => {
    expect(deriveStepRunVerdict([{ step: 1, status: "pass" }])).toBe("pass");
    expect(
      deriveStepRunVerdict([
        { step: 1, status: "pass" },
        { step: 2, status: "skip" },
      ]),
    ).toBe("pass");
  });

  it("records skip when EVERY step was skipped — nothing actually ran", () => {
    expect(
      deriveStepRunVerdict([
        { step: 1, status: "skip" },
        { step: 2, status: "skip" },
      ]),
    ).toBe("skip");
  });
});

describe("serializeStepResults / parseStepResults round trip", () => {
  it("round-trips a mixed run with a failure note", () => {
    const results: StepResult[] = [
      { step: 1, status: "pass" },
      { step: 2, status: "fail", note: "Save button stayed disabled" },
      { step: 3, status: "skip" },
    ];
    const output = serializeStepResults(results);
    expect(output).toContain("Manual step run — 1/3 passed, failed at step 2, 1 skipped");
    expect(parseStepResults(output)).toEqual(results);
  });

  it("keeps unicode notes intact", () => {
    const results: StepResult[] = [{ step: 1, status: "fail", note: "按钮不可用 → 错误" }];
    expect(parseStepResults(serializeStepResults(results))).toEqual(results);
  });

  it("trims notes and drops empty ones on serialize", () => {
    const output = serializeStepResults([
      { step: 1, status: "fail", note: "  spaced  " },
      { step: 2, status: "pass", note: "   " },
    ]);
    expect(parseStepResults(output)).toEqual([
      { step: 1, status: "fail", note: "spaced" },
      { step: 2, status: "pass" },
    ]);
  });

  it("returns null (never throws) on agent free-text, legacy and malformed outputs", () => {
    for (const output of [
      "",
      "embedded browser unreachable",
      "assertion failed: expected 200 got 500",
      "```step-results\nnot json\n```",
      "```step-results\n{\"step\":1}\n```", // object, not array
      "```step-results\n[]\n```", // empty array carries no breakdown
      "```step-results\n[{\"step\":\"one\",\"status\":\"pass\"}]\n```", // wrong types
      "```step-results\n[{\"step\":1,\"status\":\"exploded\"}]\n```", // unknown status
      "```step-results", // unterminated fence
    ]) {
      expect(() => parseStepResults(output)).not.toThrow();
      expect(parseStepResults(output)).toBeNull();
    }
  });

  it("skips malformed entries but keeps valid siblings", () => {
    const output = '```step-results\n[{"step":1,"status":"pass"},null,{"status":"fail"},{"step":3,"status":"skip"}]\n```';
    expect(parseStepResults(output)).toEqual([
      { step: 1, status: "pass" },
      { step: 3, status: "skip" },
    ]);
  });
});
