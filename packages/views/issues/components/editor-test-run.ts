import { proxyHeaders, absoluteBase } from "./editor-proxy-fetch";
import type { TestRunState } from "./editor-preview-pane";

// Shared test-run trigger — POSTs the daemon's /editor/test for a worktree and
// normalizes the result into the parent-lifted TestRunState shape (minus the
// parsed view, which the consumer derives). Extracted so both the preview pane
// and the merged App tab drive the SAME endpoint with identical result shaping,
// instead of each maintaining its own copy. Never throws — a network/HTTP error
// resolves to a done+failed result so the caller can render it inline.
export async function runEditorTest(
  daemonUrl: string,
  workdir: string,
  testCommand?: string,
): Promise<Omit<TestRunState, "parsedTests">> {
  try {
    const r = await fetch(`${absoluteBase(daemonUrl)}/editor/test`, {
      method: "POST",
      headers: proxyHeaders(daemonUrl),
      // Empty command still means "daemon auto-detects".
      body: JSON.stringify({ workdir, command: testCommand ?? "" }),
    });
    if (!r.ok) throw new Error(`could not run tests (${r.status})`);
    const d = (await r.json()) as {
      needs_command?: boolean;
      command?: string;
      passed?: boolean;
      output?: string;
    };
    if (d.needs_command) {
      return {
        testState: "done",
        testOut:
          "No test command detected (package.json / Makefile / go.mod / composer.json) — nothing to run.",
        testPassed: null,
        testCmd: "",
      };
    }
    return {
      testState: "done",
      testOut: d.output || "",
      testPassed: d.passed === true,
      testCmd: d.command || "",
    };
  } catch (e) {
    return {
      testState: "done",
      testOut: e instanceof Error ? e.message : "could not run tests",
      testPassed: false,
      testCmd: "",
    };
  }
}
