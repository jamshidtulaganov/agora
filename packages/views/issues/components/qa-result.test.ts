import { describe, it, expect } from "vitest";
import { parseQAResultBlock } from "./qa-result";

// The qa-result block is agent-authored, untrusted content. parseQAResultBlock
// must extract it deterministically when valid and fail CLOSED (return null, so
// the panel falls back to the raw view) on anything malformed — never throw into
// the UI. These tests pin that contract.
describe("parseQAResultBlock", () => {
  const wrap = (json: string) => `Some prose.\n\n\`\`\`qa-result\n${json}\n\`\`\`\nmore prose`;

  it("parses a valid block", () => {
    const r = parseQAResultBlock(
      wrap(
        JSON.stringify({
          verdict: "pass",
          summary: "no new failures",
          commands: [
            { cmd: "pnpm build", baseline_exit: 1, branch_exit: 1, kind: "pre_existing" },
            { cmd: "pnpm test -- new.spec", baseline_exit: null, branch_exit: 0, kind: "pass" },
          ],
          screenshots: ["/tmp/a.png"],
        }),
      ),
    );
    expect(r).not.toBeNull();
    expect(r!.verdict).toBe("pass");
    expect(r!.summary).toBe("no new failures");
    expect(r!.commands).toHaveLength(2);
    expect(r!.commands[1]!.baseline_exit).toBeNull();
    expect(r!.screenshots).toEqual(["/tmp/a.png"]);
  });

  it("returns null when no block is present", () => {
    expect(parseQAResultBlock("just a normal comment, exit code 0")).toBeNull();
  });

  it("returns null on invalid JSON inside the block", () => {
    expect(parseQAResultBlock(wrap("{not valid json,"))).toBeNull();
  });

  it("returns null when verdict is missing or unknown", () => {
    expect(parseQAResultBlock(wrap(JSON.stringify({ commands: [] })))).toBeNull();
    expect(
      parseQAResultBlock(wrap(JSON.stringify({ verdict: "maybe", commands: [] }))),
    ).toBeNull();
  });

  it("drops malformed command rows and coerces missing fields", () => {
    const r = parseQAResultBlock(
      wrap(
        JSON.stringify({
          verdict: "fail",
          commands: [
            { cmd: "go test ./...", branch_exit: 1, kind: "new_failure" }, // no baseline_exit
            { baseline_exit: 0, branch_exit: 0, kind: "pass" }, // no cmd → dropped
            "garbage", // not an object → dropped
            { cmd: "lint", branch_exit: 2, kind: "bogus" }, // bad kind → defaults to pass
          ],
        }),
      ),
    );
    expect(r).not.toBeNull();
    expect(r!.commands).toHaveLength(2);
    expect(r!.commands[0]!.baseline_exit).toBeNull();
    expect(r!.commands[0]!.kind).toBe("new_failure");
    expect(r!.commands[1]!.kind).toBe("pass");
    expect(r!.screenshots).toEqual([]);
  });

  it("ignores non-string screenshots", () => {
    const r = parseQAResultBlock(
      wrap(JSON.stringify({ verdict: "pass", commands: [], screenshots: ["ok", 5, null] })),
    );
    expect(r!.screenshots).toEqual(["ok"]);
  });
});
