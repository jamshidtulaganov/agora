import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import type { Issue } from "@agora/core/types";
import { BoardChildIssueList } from "./board-card";

const child = (id: string, identifier: string, title: string, status: Issue["status"]): Issue => ({
  id,
  workspace_id: "workspace-1",
  number: Number(identifier.split("-")[1]),
  identifier,
  title,
  description: null,
  status,
  priority: "none",
  assignee_type: null,
  assignee_id: null,
  creator_type: "member",
  creator_id: "user-1",
  parent_issue_id: "parent-1",
  project_id: "project-1",
  position: 0,
  start_date: null,
  due_date: null,
  metadata: {},
  created_at: "2026-08-11T00:00:00Z",
  updated_at: "2026-08-11T00:00:00Z",
});

describe("BoardChildIssueList", () => {
  it("renders compact child status rows and the hidden remainder", () => {
    render(
      <BoardChildIssueList
        label="Sub-issues"
        issues={[
          child("child-1", "ISSUE-29", "ServerCRM EFS compatibility", "in_review"),
          child("child-2", "ISSUE-30", "Mytrion card automations", "in_progress"),
        ]}
        remainingCount={3}
      />,
    );

    expect(screen.getByLabelText("Sub-issues")).toBeInTheDocument();
    expect(screen.getByText("ISSUE-29")).toBeInTheDocument();
    expect(screen.getByText("Mytrion card automations")).toBeInTheDocument();
    expect(screen.getByText("+3")).toBeInTheDocument();
  });
});
