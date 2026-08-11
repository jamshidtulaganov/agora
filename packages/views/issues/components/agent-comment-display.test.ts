import { describe, expect, it } from "vitest";
import {
  humanReadableAgentComment,
  isRedundantAgentCompletionFailure,
} from "./agent-comment-display";

describe("humanReadableAgentComment", () => {
  it("keeps human prose and removes machine-only blocks", () => {
    const content = [
      "Review finished. Two checks could not run.",
      "```todo\n- [x] review\n```",
      "```agora-handoff\n{\"schema_version\":1,\"summary\":\"Review finished\",\"contracts\":[]}\n```",
    ].join("\n\n");

    expect(humanReadableAgentComment(content, "agent")).toBe(
      "Review finished. Two checks could not run.",
    );
  });

  it("uses the handoff summary when the block is the whole comment", () => {
    const content = "```agora-handoff\n{\"schema_version\":1,\"summary\":\"QA passed\"}\n```";
    expect(humanReadableAgentComment(content, "agent")).toBe("QA passed");
  });

  it("never rewrites member-authored markdown", () => {
    const content = "```todo\nkeep my checklist\n```";
    expect(humanReadableAgentComment(content, "member")).toBe(content);
  });
});

describe("isRedundantAgentCompletionFailure", () => {
  it("matches the daemon 409 already represented by task_failed activity", () => {
    const content =
      'complete task failed: POST /api/daemon/tasks/id/complete returned 409: {"error":"verification worktree changed or no longer matches the integrated HEAD"}';
    expect(isRedundantAgentCompletionFailure(content, "agent")).toBe(true);
    expect(isRedundantAgentCompletionFailure(content, "member")).toBe(false);
  });
});
