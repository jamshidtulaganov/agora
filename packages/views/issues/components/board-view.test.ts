import { describe, expect, it } from "vitest";
import type { Issue } from "@agora/core/types";
import { topLevelBoardIssues } from "./board-view";

const issue = (id: string, parentIssueId: string | null): Issue => ({
  id,
  workspace_id: "workspace-1",
  number: 1,
  identifier: id,
  title: id,
  description: null,
  status: "todo",
  priority: "none",
  assignee_type: null,
  assignee_id: null,
  creator_type: "member",
  creator_id: "user-1",
  parent_issue_id: parentIssueId,
  project_id: null,
  position: 0,
  start_date: null,
  due_date: null,
  metadata: {},
  created_at: "2026-08-11T00:00:00Z",
  updated_at: "2026-08-11T00:00:00Z",
});

describe("topLevelBoardIssues", () => {
  it("keeps parents on the board and removes duplicate full-size child cards", () => {
    expect(topLevelBoardIssues([
      issue("ISSUE-10", null),
      issue("ISSUE-29", "parent-10"),
      issue("ISSUE-30", "parent-10"),
    ]).map((item) => item.id)).toEqual(["ISSUE-10"]);
  });
});
