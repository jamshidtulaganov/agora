import { describe, expect, it } from "vitest";
import type { AssistantMessage } from "@agora/core/types";
import { autoOpenDecision, isArtifactUpdating, runArtifactId } from "./artifact-workbench";

function message(overrides: Partial<AssistantMessage>): AssistantMessage {
  return {
    id: "m-1",
    session_id: "s-1",
    role: "assistant",
    content: "",
    created_at: "2026-09-16T10:00:00Z",
    ...overrides,
  };
}

function artifactToolMessage(id: string, artifactId: string, name = "create_artifact") {
  return message({
    id,
    role: "tool",
    tool_name: name,
    tool_result: { artifact_id: artifactId, title: "Report", kind: "markdown", version: 1 },
  });
}

describe("runArtifactId", () => {
  const userMessage = message({ id: "u-1", role: "user", content: "make me a report" });

  it("returns the LAST artifact the run produced", () => {
    const messages = [
      artifactToolMessage("t-0", "art-old"),
      userMessage,
      artifactToolMessage("t-1", "art-1"),
      artifactToolMessage("t-2", "art-2", "update_artifact"),
    ];

    expect(runArtifactId(messages, "u-1")).toBe("art-2");
  });

  it("ignores artifacts produced BEFORE the run started", () => {
    const messages = [artifactToolMessage("t-0", "art-old"), userMessage];

    expect(runArtifactId(messages, "u-1")).toBeNull();
  });

  it("attributes nothing when the run's own message isn't in the cache yet", () => {
    expect(runArtifactId([artifactToolMessage("t-1", "art-1")], "u-1")).toBeNull();
    expect(runArtifactId([], null)).toBeNull();
  });

  it("skips tool rows whose result isn't a recognisable artifact", () => {
    const messages = [
      userMessage,
      artifactToolMessage("t-1", "art-1"),
      message({ id: "t-2", role: "tool", tool_name: "create_artifact", tool_result: { error: "nope" } }),
      message({ id: "t-3", role: "tool", tool_name: "list_issues", tool_result: { artifact_id: "not-an-artifact" } }),
    ];

    expect(runArtifactId(messages, "u-1")).toBe("art-1");
  });
});

describe("autoOpenDecision — once per run", () => {
  const base = {
    runId: "run-1",
    artifactId: "art-1",
    lastAutoOpenedRunId: null as string | null,
    openArtifactId: null as string | null,
  };

  it("opens the first artifact a run produces", () => {
    expect(autoOpenDecision(base)).toEqual({ artifactId: "art-1", spentRunId: "run-1" });
  });

  it("never opens twice for the same run", () => {
    const first = autoOpenDecision(base);
    // The agent writes a second artifact in the same turn.
    const second = autoOpenDecision({
      ...base,
      artifactId: "art-2",
      lastAutoOpenedRunId: first.spentRunId,
      openArtifactId: "art-1",
    });

    expect(second).toEqual({ artifactId: null, spentRunId: null });
  });

  it("does not steal a pane the user switched during the same run", () => {
    // A manual open spends the run's budget (the hook records it), so the
    // artifact the run produces next leaves the user's choice alone.
    const decision = autoOpenDecision({
      ...base,
      artifactId: "art-2",
      lastAutoOpenedRunId: "run-1",
      openArtifactId: "art-9",
    });

    expect(decision.artifactId).toBeNull();
  });

  it("spends the budget without switching when the artifact is already open", () => {
    expect(autoOpenDecision({ ...base, openArtifactId: "art-1" })).toEqual({
      artifactId: null,
      spentRunId: "run-1",
    });
  });

  it("gives the NEXT run its own auto-open", () => {
    expect(
      autoOpenDecision({
        runId: "run-2",
        artifactId: "art-2",
        lastAutoOpenedRunId: "run-1",
        openArtifactId: "art-1",
      }),
    ).toEqual({ artifactId: "art-2", spentRunId: "run-2" });
  });

  it("decides nothing without a run or an artifact", () => {
    expect(autoOpenDecision({ ...base, runId: null })).toEqual({ artifactId: null, spentRunId: null });
    expect(autoOpenDecision({ ...base, artifactId: null })).toEqual({ artifactId: null, spentRunId: null });
  });
});

describe("isArtifactUpdating", () => {
  const base = {
    isRunning: true,
    activeTool: "update_artifact",
    openArtifactId: "art-1",
    runArtifactId: "art-1" as string | null,
  };

  it("flags the open artifact while the run rewrites it", () => {
    expect(isArtifactUpdating(base)).toBe(true);
    expect(isArtifactUpdating({ ...base, activeTool: "create_artifact" })).toBe(true);
    // A run that hasn't touched an artifact yet is assumed to be writing the
    // one on screen — the tool_result that would name the target doesn't
    // exist until the call returns.
    expect(isArtifactUpdating({ ...base, runArtifactId: null })).toBe(true);
  });

  it("stays quiet for another artifact, another tool, or a finished run", () => {
    expect(isArtifactUpdating({ ...base, runArtifactId: "art-2" })).toBe(false);
    expect(isArtifactUpdating({ ...base, activeTool: "list_issues" })).toBe(false);
    expect(isArtifactUpdating({ ...base, activeTool: null })).toBe(false);
    expect(isArtifactUpdating({ ...base, isRunning: false })).toBe(false);
    expect(isArtifactUpdating({ ...base, openArtifactId: null })).toBe(false);
  });
});
