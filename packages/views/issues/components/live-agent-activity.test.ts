import { describe, expect, it } from "vitest";
import type { TimelineItem } from "../../common/task-transcript";
import {
  deriveActivitySteps,
  deriveCurrentActivity,
  deriveFileChanges,
} from "./live-agent-activity";

function tool(seq: number, name: string, input: Record<string, unknown>): TimelineItem {
  return { seq, type: "tool_use", tool: name, input };
}

describe("deriveCurrentActivity", () => {
  it("returns null for an empty transcript", () => {
    expect(deriveCurrentActivity([])).toBeNull();
  });

  it("maps Read to the 'reading' verb with a shortened file path", () => {
    const a = deriveCurrentActivity([
      tool(1, "Read", { file_path: "/home/u/project/src/features/geo/geoApi.ts" }),
    ]);
    expect(a).toEqual({ verbKey: "reading", rawVerb: undefined, target: ".../geo/geoApi.ts" });
  });

  it("maps Edit and Write to editing / writing", () => {
    expect(deriveCurrentActivity([tool(1, "Edit", { file_path: "routes.ts" })])?.verbKey).toBe(
      "editing",
    );
    expect(deriveCurrentActivity([tool(1, "Write", { file_path: "a/b/new.ts" })])?.verbKey).toBe(
      "writing",
    );
  });

  it("maps Bash to 'running' with the command as target (clamped)", () => {
    const a = deriveCurrentActivity([tool(1, "Bash", { command: "npm run type-check" })]);
    expect(a?.verbKey).toBe("running");
    expect(a?.target).toBe("npm run type-check");
  });

  it("maps Grep/Glob to 'searching' using pattern", () => {
    const a = deriveCurrentActivity([tool(1, "Grep", { pattern: "TODO" })]);
    expect(a?.verbKey).toBe("searching");
    expect(a?.target).toBe("TODO");
  });

  it("falls back to the raw tool name for an unmapped tool", () => {
    const a = deriveCurrentActivity([tool(1, "Deploy", { description: "ship it" })]);
    expect(a?.verbKey).toBeNull();
    expect(a?.rawVerb).toBe("Deploy");
    expect(a?.target).toBe("ship it");
  });

  it("prefers the most recent tool_use, skipping a trailing tool_result", () => {
    const items: TimelineItem[] = [
      tool(1, "Read", { file_path: "old.ts" }),
      tool(2, "Edit", { file_path: "routes.ts" }),
      { seq: 3, type: "tool_result", tool: "Edit", output: "ok" },
    ];
    const a = deriveCurrentActivity(items);
    expect(a?.verbKey).toBe("editing");
    expect(a?.target).toBe("routes.ts");
  });

  it("reflects a thinking tail when no tool has run yet", () => {
    const a = deriveCurrentActivity([{ seq: 1, type: "thinking", content: "planning" }]);
    expect(a?.verbKey).toBe("thinking");
  });

  it("reflects a text tail as 'working'", () => {
    const a = deriveCurrentActivity([{ seq: 1, type: "text", content: "Here is the plan" }]);
    expect(a?.verbKey).toBe("working");
  });
});

describe("deriveFileChanges", () => {
  it("returns an empty list when no file was mutated", () => {
    expect(deriveFileChanges([])).toEqual([]);
    expect(
      deriveFileChanges([
        tool(1, "Read", { file_path: "a.ts" }),
        tool(2, "Bash", { command: "ls" }),
        tool(3, "Grep", { pattern: "TODO" }),
        { seq: 4, type: "thinking", content: "hmm" },
      ]),
    ).toEqual([]);
  });

  it("excludes every non-mutating tool, keeping only writes/edits", () => {
    const changes = deriveFileChanges([
      tool(1, "Read", { file_path: "a.ts" }),
      tool(2, "Edit", { file_path: "b.ts", old_string: "x", new_string: "y" }),
      tool(3, "Glob", { pattern: "**/*.ts" }),
      tool(4, "WebFetch", { url: "https://x" }),
    ]);
    expect(changes.map((c) => c.path)).toEqual(["b.ts"]);
  });

  it("maps Write to a whole-file add with content line counts", () => {
    const [c] = deriveFileChanges([
      tool(1, "Write", {
        file_path: "/home/u/project/src/geo/geoApi.ts",
        content: "line1\nline2\nline3",
      }),
    ]);
    expect(c).toMatchObject({
      key: "idx-0",
      path: "/home/u/project/src/geo/geoApi.ts",
      shortPath: ".../geo/geoApi.ts",
      kind: "write",
      added: "line1\nline2\nline3",
      removed: "",
      additions: 3,
      deletions: 0,
    });
  });

  it("maps Edit to old/new strings with both line counts", () => {
    const [c] = deriveFileChanges([
      tool(1, "Edit", {
        file_path: "routes.ts",
        old_string: "a\nb",
        new_string: "a\nb\nc\nd",
      }),
    ]);
    expect(c).toMatchObject({
      kind: "edit",
      added: "a\nb\nc\nd",
      removed: "a\nb",
      additions: 4,
      deletions: 2,
    });
  });

  it("concatenates MultiEdit sub-edits and sums their counts", () => {
    const [c] = deriveFileChanges([
      tool(1, "MultiEdit", {
        file_path: "app.ts",
        edits: [
          { old_string: "a", new_string: "A\nB" },
          { old_string: "c\nd", new_string: "C" },
        ],
      }),
    ]);
    expect(c).toMatchObject({
      kind: "edit",
      added: "A\nB\nC",
      removed: "a\nc\nd",
      additions: 3,
      deletions: 3,
    });
  });

  it("treats NotebookEdit as an edit on notebook_path using new_source", () => {
    const [c] = deriveFileChanges([
      tool(1, "NotebookEdit", {
        notebook_path: "nb.ipynb",
        new_source: "print(1)\nprint(2)",
      }),
    ]);
    expect(c).toMatchObject({
      path: "nb.ipynb",
      kind: "edit",
      added: "print(1)\nprint(2)",
      additions: 2,
      deletions: 0,
    });
  });

  it("matches tool names case-insensitively", () => {
    const changes = deriveFileChanges([
      tool(1, "WRITE", { file_path: "x.ts", content: "a" }),
      tool(2, "edit", { file_path: "y.ts", old_string: "a", new_string: "b" }),
    ]);
    expect(changes.map((c) => c.path)).toEqual(["y.ts", "x.ts"]);
  });

  it("returns changes newest first with stable index keys", () => {
    const changes = deriveFileChanges([
      tool(1, "Write", { file_path: "first.ts", content: "1" }),
      tool(2, "Read", { file_path: "skip.ts" }),
      tool(3, "Edit", { file_path: "second.ts", old_string: "a", new_string: "b" }),
    ]);
    expect(changes.map((c) => [c.path, c.key])).toEqual([
      ["second.ts", "idx-2"],
      ["first.ts", "idx-0"],
    ]);
  });

  it("skips a mutating call that carries no path", () => {
    expect(deriveFileChanges([tool(1, "Edit", { old_string: "a", new_string: "b" })])).toEqual([]);
  });
});

describe("deriveActivitySteps", () => {
  it("returns an empty list for an empty or tool-less transcript", () => {
    expect(deriveActivitySteps([])).toEqual([]);
    expect(
      deriveActivitySteps([
        { seq: 1, type: "text", content: "thinking out loud" },
        { seq: 2, type: "thinking", content: "hmm" },
      ]),
    ).toEqual([]);
  });

  it("maps non-mutating tools to verb + target, newest first", () => {
    const steps = deriveActivitySteps([
      tool(1, "Read", { file_path: "/p/src/wexRoutes.js" }),
      tool(2, "Grep", { pattern: "cron schedule" }),
    ]);
    expect(steps).toEqual([
      { key: "idx-1", verbKey: "searching", rawVerb: undefined, target: "cron schedule" },
      { key: "idx-0", verbKey: "reading", rawVerb: undefined, target: ".../src/wexRoutes.js" },
    ]);
  });

  it("summarizes known agora/gh commands instead of dumping the raw command", () => {
    const steps = deriveActivitySteps([
      tool(1, "Bash", { command: "agora issue comment add a066a9c7 --content-stdin <<'C'" }),
      tool(2, "Bash", { command: "agora issue status a066a9c7 in_review" }),
      tool(3, "Bash", { command: "gh pr create --fill" }),
    ]);
    expect(steps.map((s) => s.target)).toEqual([
      "gh pr create",
      "agora issue status → in_review",
      "agora issue comment",
    ]);
    expect(steps.every((s) => s.verbKey === "running")).toBe(true);
  });

  it("clamps an unknown command to a verbatim summary", () => {
    const [s] = deriveActivitySteps([tool(1, "Bash", { command: "npm run build" })]);
    expect(s).toMatchObject({ verbKey: "running", target: "npm run build" });
  });

  it("collapses consecutive identical steps", () => {
    const steps = deriveActivitySteps([
      tool(1, "Grep", { pattern: "cron" }),
      tool(2, "Grep", { pattern: "cron" }),
      tool(3, "Grep", { pattern: "schedule" }),
    ]);
    expect(steps.map((s) => [s.key, s.target])).toEqual([
      ["idx-2", "schedule"],
      ["idx-0", "cron"],
    ]);
  });

  it("falls back to the raw tool name for an unmapped tool", () => {
    const [s] = deriveActivitySteps([tool(1, "Deploy", { description: "ship it" })]);
    expect(s).toMatchObject({ verbKey: null, rawVerb: "Deploy", target: "ship it" });
  });

  it("translates Playwright MCP browser tools into human actions (not raw tool ids)", () => {
    const steps = deriveActivitySteps([
      tool(1, "mcp__playwright__browser_navigate", { url: "https://agora-cs.sdteam.uz/user/login" }),
      tool(2, "mcp__plugin_playwright_playwright__browser_fill_form", { fields: [] }),
      tool(3, "mcp__playwright__browser_click", { element: "Voyti submit button" }),
      tool(4, "mcp__MCP_DOCKER__browser_snapshot", {}),
      tool(5, "mcp__playwright__browser_console_messages", { level: "error" }),
    ]);
    // newest-first
    expect(steps.map((s) => [s.rawVerb, s.target])).toEqual([
      ["read console", "error"],
      ["read page", ""],
      ["click", "Voyti submit button"],
      ["fill", "form"],
      ["open", "https://agora-cs.sdteam.uz/user/login"],
    ]);
    expect(steps.every((s) => !(s.rawVerb ?? "").includes("mcp__"))).toBe(true);
    expect(steps.every((s) => s.verbKey === null)).toBe(true);
  });
});
