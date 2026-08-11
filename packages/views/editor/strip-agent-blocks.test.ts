import { describe, expect, it } from "vitest";
import { stripAgentMachineBlocks } from "./readonly-content";

describe("stripAgentMachineBlocks", () => {
  it("removes a qa-result block but keeps the human report", () => {
    const c = [
      "## QA Verdict: PASS",
      "All checks green, no new failures.",
      "",
      "```qa-result",
      '{"verdict":"pass","summary":"ok","commands":[]}',
      "```",
    ].join("\n");
    const out = stripAgentMachineBlocks(c);
    expect(out).toContain("QA Verdict: PASS");
    expect(out).toContain("no new failures");
    expect(out).not.toContain("qa-result");
    expect(out).not.toContain("verdict");
  });

  it("strips design-proposal / test-cases / knowledge-items too", () => {
    for (const lang of ["design-proposal", "test-cases", "knowledge-items", "design-context", "design-manifest"]) {
      const c = `Summary line.\n\n\`\`\`${lang}\n{"x":1}\n\`\`\``;
      const out = stripAgentMachineBlocks(c);
      expect(out).toBe("Summary line.");
    }
  });

  it("leaves ordinary code fences (js/go/json) untouched", () => {
    const c = "Here is code:\n\n```ts\nconst a = 1;\n```";
    expect(stripAgentMachineBlocks(c)).toBe(c);
    const j = "```json\n{\"a\":1}\n```";
    expect(stripAgentMachineBlocks(j)).toBe(j);
  });

  it("handles multiple machine blocks in one comment", () => {
    const c = "Done.\n\n```test-cases\n[]\n```\n\n```knowledge-items\n[]\n```";
    expect(stripAgentMachineBlocks(c)).toBe("Done.");
  });
});
