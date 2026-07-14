import { describe, expect, it } from "vitest";
import type { TimelineItem } from "../../common/task-transcript";
import {
  classifyCommand,
  deriveActivitySteps,
  deriveCurrentActivity,
  deriveFileChanges,
  deriveFileDocs,
  deriveMilestoneSteps,
  deriveProgressHeadline,
  deriveTodos,
  unwrapShell,
  FRAGMENT_SEPARATOR,
} from "./live-agent-activity";

function text(seq: number, content: string): TimelineItem {
  return { seq, type: "text", content };
}

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

describe("deriveFileDocs", () => {
  it("returns an empty list when nothing was mutated", () => {
    expect(deriveFileDocs([tool(1, "Read", { file_path: "a.ts" })])).toEqual([]);
  });

  it("a Write yields a full (non-partial) doc with the whole file highlighted", () => {
    const [doc] = deriveFileDocs([
      tool(1, "Write", { file_path: "src/chart.tsx", content: "line1\nline2\nline3" }),
    ]);
    expect(doc).toMatchObject({
      path: "src/chart.tsx",
      text: "line1\nline2\nline3",
      partial: false,
      ranges: [{ from: 0, count: 3 }],
    });
  });

  it("an anchored Edit replaces in place and highlights the replacement lines", () => {
    const docs = deriveFileDocs([
      tool(1, "Write", { file_path: "a.ts", content: "aaa\nbbb\nccc" }),
      tool(2, "Edit", { file_path: "a.ts", old_string: "bbb", new_string: "BBB\nBBB2" }),
    ]);
    expect(docs).toHaveLength(1);
    expect(docs[0]).toMatchObject({
      text: "aaa\nBBB\nBBB2\nccc",
      partial: false,
      ranges: [{ from: 1, count: 2 }],
    });
  });

  it("an unanchored Edit appends a separator-delimited fragment and marks the doc partial", () => {
    const [doc] = deriveFileDocs([
      tool(1, "Edit", { file_path: "b.ts", old_string: "never-seen", new_string: "x\ny" }),
    ]);
    expect(doc).toMatchObject({ partial: true, ranges: [{ from: 0, count: 2 }] });
    expect(doc!.text).toBe("x\ny");

    const [doc2] = deriveFileDocs([
      tool(1, "Edit", { file_path: "b.ts", old_string: "n1", new_string: "x\ny" }),
      tool(2, "Edit", { file_path: "b.ts", old_string: "n2", new_string: "z" }),
    ]);
    expect(doc2!.text).toBe(`x\ny\n${FRAGMENT_SEPARATOR}\nz`);
    // lines: 0=x 1=y 2=sep 3=z
    expect(doc2!.ranges).toEqual([{ from: 3, count: 1 }]);
    expect(doc2!.partial).toBe(true);
  });

  it("MultiEdit applies its pairs sequentially against a written base", () => {
    const [doc] = deriveFileDocs([
      tool(1, "Write", { file_path: "c.ts", content: "one\ntwo\nthree" }),
      tool(2, "MultiEdit", {
        file_path: "c.ts",
        edits: [
          { old_string: "one", new_string: "ONE" },
          { old_string: "three", new_string: "THREE" },
        ],
      }),
    ]);
    expect(doc!.text).toBe("ONE\ntwo\nTHREE");
    expect(doc!.partial).toBe(false);
    expect(doc!.ranges).toEqual([
      { from: 0, count: 1 },
      { from: 2, count: 1 },
    ]);
  });

  it("only the newest mutation of a file is highlighted, and docs order newest-first", () => {
    const docs = deriveFileDocs([
      tool(1, "Write", { file_path: "first.ts", content: "f" }),
      tool(2, "Write", { file_path: "second.ts", content: "s1\ns2" }),
      tool(3, "Edit", { file_path: "first.ts", old_string: "f", new_string: "F" }),
    ]);
    expect(docs.map((d) => d.path)).toEqual(["first.ts", "second.ts"]);
    expect(docs[0]!.ranges).toEqual([{ from: 0, count: 1 }]);
    expect(docs[0]!.text).toBe("F");
  });

  it("a pure deletion updates text without a highlight", () => {
    const [doc] = deriveFileDocs([
      tool(1, "Write", { file_path: "d.ts", content: "keep\ndrop\n" }),
      tool(2, "Edit", { file_path: "d.ts", old_string: "drop\n", new_string: "" }),
    ]);
    expect(doc!.text).toBe("keep\n");
    expect(doc!.ranges).toEqual([]);
  });
});

describe("deriveProgressHeadline", () => {
  it("returns the latest PROGRESS: line the agent emitted", () => {
    expect(
      deriveProgressHeadline([
        text(1, "Let me start.\nPROGRESS: Reading the greet button component"),
        tool(2, "Read", { file_path: "Button.tsx" }),
        text(3, "PROGRESS: Running the tests\nall good"),
      ]),
    ).toBe("Running the tests");
  });

  it("only matches the marker at the start of a line", () => {
    expect(
      deriveProgressHeadline([text(1, "there was no progress: here at all")]),
    ).toBeNull();
  });

  it("returns null when no headline was emitted", () => {
    expect(deriveProgressHeadline([tool(1, "Bash", { command: "ls" })])).toBeNull();
  });
});

describe("deriveTodos", () => {
  it("parses the latest TodoWrite into an ordered plan", () => {
    const todos = deriveTodos([
      tool(1, "TodoWrite", {
        todos: [{ content: "Old plan", status: "pending" }],
      }),
      tool(2, "TodoWrite", {
        todos: [
          { content: "Read the code", status: "completed" },
          { content: "Fix the typo", activeForm: "Fixing the typo", status: "in_progress" },
          { content: "Run the tests", status: "pending" },
        ],
      }),
    ]);
    expect(todos).toEqual([
      { content: "Read the code", status: "completed" },
      { content: "Fix the typo", status: "in_progress", activeForm: "Fixing the typo" },
      { content: "Run the tests", status: "pending" },
    ]);
  });

  it("handles the normalized todo_write tool name and active_form key", () => {
    const todos = deriveTodos([
      tool(1, "todo_write", {
        todos: [{ content: "Ship it", status: "in_progress", active_form: "Shipping it" }],
      }),
    ]);
    expect(todos[0]).toEqual({ content: "Ship it", status: "in_progress", activeForm: "Shipping it" });
  });

  it("degrades unknown statuses to pending and skips empty entries", () => {
    const todos = deriveTodos([
      tool(1, "TodoWrite", {
        todos: [
          { content: "A", status: "bogus" },
          { content: "", status: "pending" },
          null,
          { status: "completed" },
        ],
      }),
    ]);
    expect(todos).toEqual([{ content: "A", status: "pending" }]);
  });

  it("returns [] when there's no todo tool or the payload is malformed", () => {
    expect(deriveTodos([tool(1, "Bash", { command: "ls" })])).toEqual([]);
    expect(deriveTodos([tool(1, "TodoWrite", { todos: "nope" })])).toEqual([]);
  });

  it("parses a fenced ```todo block from text (providers without a todo tool)", () => {
    const todos = deriveTodos([
      text(1, "PROGRESS: Fixing the typo\n```todo\n- [x] Read the code\n- [~] Fix the typo\n- [ ] Run the tests\n```"),
    ]);
    expect(todos).toEqual([
      { content: "Read the code", status: "completed" },
      { content: "Fix the typo", status: "in_progress" },
      { content: "Run the tests", status: "pending" },
    ]);
  });

  it("uses the LATEST fenced ```todo block (each rewrites the list)", () => {
    const todos = deriveTodos([
      text(1, "```todo\n- [~] Read the code\n- [ ] Fix the typo\n```"),
      text(2, "```todo\n- [x] Read the code\n- [x] Fix the typo\n- [~] Run the tests\n```"),
    ]);
    expect(todos).toEqual([
      { content: "Read the code", status: "completed" },
      { content: "Fix the typo", status: "completed" },
      { content: "Run the tests", status: "in_progress" },
    ]);
  });

  it("prefers a TodoWrite tool call over a text block, but falls back to the block", () => {
    // Tool present → wins.
    expect(
      deriveTodos([
        text(1, "```todo\n- [ ] from text\n```"),
        tool(2, "TodoWrite", { todos: [{ content: "from tool", status: "pending" }] }),
      ]),
    ).toEqual([{ content: "from tool", status: "pending" }]);
    // Only a malformed tool → falls through to the block.
    expect(
      deriveTodos([
        text(1, "```todo\n- [~] from text\n```"),
        tool(2, "TodoWrite", { todos: "nope" }),
      ]),
    ).toEqual([{ content: "from text", status: "in_progress" }]);
  });
});

describe("unwrapShell", () => {
  it("unwraps a login-shell invocation to the inner command", () => {
    expect(unwrapShell("/bin/zsh -lc 'git push -u origin agent/x'")).toBe(
      "git push -u origin agent/x",
    );
    expect(unwrapShell('/bin/bash -c "npm test"')).toBe("npm test");
    expect(unwrapShell("sh -c 'ls -la'")).toBe("ls -la");
  });

  it("returns the command unchanged when there's no shell wrapper", () => {
    expect(unwrapShell("git status --short")).toBe("git status --short");
  });
});

describe("exec_command and shell-tool variants", () => {
  it("routes exec_command through summarize/classify (not a raw dump)", () => {
    const [s] = deriveActivitySteps([
      tool(1, "exec_command", {
        command: "/bin/zsh -lc 'git push -u origin agent/dev-bot/47245d4f'",
      }),
    ]);
    // Not the raw "/bin/zsh -lc …" wrapper, and not a rawVerb tool-name dump.
    expect(s?.verbKey).toBe("running");
    expect(s?.rawVerb).toBeUndefined();
    expect(s?.cmdClass).toBe("publish");
    expect(s?.target).toBe("git push");
  });

  it("classifies a wrapped git commit as the commit milestone", () => {
    expect(
      classifyCommand(unwrapShell(`/bin/zsh -lc 'git add . && git commit -m "x"'`)),
    ).toBe("commit");
  });
});

describe("deriveMilestoneSteps", () => {
  it("keeps only human milestones and drops git/inspect plumbing", () => {
    const steps = deriveMilestoneSteps([
      tool(1, "exec_command", { command: "/bin/zsh -lc 'git status --short'" }),
      tool(2, "exec_command", { command: "/bin/zsh -lc 'git diff --check'" }),
      tool(3, "exec_command", { command: "/bin/zsh -lc 'rm agora-spacing-verify.png'" }),
      tool(4, "exec_command", { command: "/bin/zsh -lc 'git branch --show-current'" }),
      tool(5, "exec_command", { command: "/bin/zsh -lc 'git commit -m \"fix\"'" }),
      tool(6, "exec_command", { command: "/bin/zsh -lc 'git push -u origin agent/x'" }),
    ]);
    // Newest first: push, then commit. The four plumbing peeks are dropped.
    expect(steps.map((s) => s.cmdClass)).toEqual(["publish", "commit"]);
  });

  it("keeps test/build/install runs and drops bare reads", () => {
    const steps = deriveMilestoneSteps([
      tool(1, "Read", { file_path: "src/a.ts" }),
      tool(2, "Bash", { command: "npm test 2>&1 | tail -50" }),
      tool(3, "Grep", { pattern: "TODO" }),
    ]);
    expect(steps.map((s) => s.cmdClass)).toEqual(["test"]);
  });

  it("returns nothing for a read-only / plumbing-only run", () => {
    expect(
      deriveMilestoneSteps([
        tool(1, "Read", { file_path: "a.ts" }),
        tool(2, "Bash", { command: "git status" }),
      ]),
    ).toEqual([]);
  });
});

describe("classifyCommand", () => {
  it("classifies dependency installs", () => {
    expect(classifyCommand("npm ci 2>&1 | tail -10")).toBe("install");
    expect(classifyCommand("pnpm install --frozen-lockfile")).toBe("install");
    expect(classifyCommand("composer install --no-dev")).toBe("install");
  });

  it("classifies test runs (incl. script names and runners)", () => {
    expect(classifyCommand("npm run test:e2e 2>&1 | tail -100")).toBe("test");
    expect(classifyCommand("npm test")).toBe("test");
    expect(classifyCommand("npx playwright test e2e/login.spec.ts")).toBe("test");
    expect(classifyCommand("go test ./...")).toBe("test");
  });

  it("classifies lint / build / git review / branch prep", () => {
    expect(classifyCommand("npm run lint:check 2>&1")).toBe("lint");
    expect(classifyCommand("npm run build")).toBe("build");
    expect(classifyCommand("git diff origin/main...HEAD -- src/a.ts")).toBe("review");
    expect(classifyCommand("git stash list && git log --oneline -3")).toBe("review");
    expect(classifyCommand("git checkout origin/main -- .")).toBe("branch");
  });

  it("classifies agent plumbing peeks as inspect", () => {
    expect(classifyCommand("wc -l /tmp/claude-0/-data-workspaces-x/tasks/a1.output")).toBe("inspect");
    expect(classifyCommand("ls /tmp/claude-0/whatever")).toBe("inspect");
    expect(classifyCommand("sleep 30 && tail -80 /tmp/claude-0/x.log")).toBe("inspect");
    expect(classifyCommand('ps aux | grep -E "(vitest|playwright)"')).toBe("inspect");
  });

  it("returns null for unknown commands (falls back to the summarized text)", () => {
    expect(classifyCommand("agora issue comment MUL-1 'done'")).toBeNull();
    expect(classifyCommand("curl -s https://example.com")).toBeNull();
  });

  it("rides into deriveActivitySteps as cmdClass with the summary kept in target", () => {
    const steps = deriveActivitySteps([
      tool(1, "Bash", { command: "npm ci 2>&1 | tail -10" }),
      tool(2, "Read", { file_path: "/tmp/claude-0/x/tasks/ab12cd.output" }),
    ]);
    // newest-first
    expect(steps[0]).toMatchObject({ verbKey: "reading", cmdClass: "inspect" });
    expect(steps[1]).toMatchObject({ verbKey: "running", cmdClass: "install" });
    expect(steps[1]!.target).toContain("npm ci");
  });
});
