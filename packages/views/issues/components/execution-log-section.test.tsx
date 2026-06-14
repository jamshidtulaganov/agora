// @vitest-environment jsdom

import { cleanup, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentTask } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";

const mockState = vi.hoisted(() => ({
  taskMessagesOptions: vi.fn(),
}));

vi.mock("@multica/core/chat/queries", () => ({
  taskMessagesOptions: mockState.taskMessagesOptions,
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <span data-testid="actor-avatar" />,
}));

vi.mock("../../common/task-transcript", () => ({
  TranscriptButton: ({ title }: { title?: string }) => (
    <button type="button">{title ?? "Transcript"}</button>
  ),
}));

vi.mock("./terminate-task-confirm-dialog", () => ({
  TerminateTaskConfirmDialog: () => null,
}));

import { ActiveTaskRow, PastRow } from "./execution-log-section";

function makeTask(overrides: Partial<AgentTask> = {}): AgentTask {
  return {
    id: "task-1",
    agent_id: "agent-1",
    runtime_id: "runtime-1",
    issue_id: "issue-1",
    status: "running",
    priority: 0,
    dispatched_at: null,
    started_at: "2026-06-08T08:00:00Z",
    completed_at: null,
    result: null,
    error: null,
    created_at: "2026-06-08T08:00:00Z",
    trigger_summary: "Started from comment",
    ...overrides,
  };
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-06-08T08:05:04Z"));
});

afterEach(() => {
  vi.useRealTimers();
});

describe("ActiveTaskRow", () => {
  it("renders running status as elapsed time only", () => {
    renderWithI18n(<ActiveTaskRow task={makeTask()} issueId="issue-1" />);

    expect(screen.getByText("5m 04s")).toBeInTheDocument();
    expect(screen.queryByText(/events?/i)).not.toBeInTheDocument();
    expect(screen.getByText("Started from comment")).toBeInTheDocument();
    expect(screen.getByText("View transcript")).toBeInTheDocument();
    expect(mockState.taskMessagesOptions).not.toHaveBeenCalled();
  });
});

describe("PastRow View pull request", () => {
  it("renders a View-PR link when a completed task's result carries a pr_url", () => {
    const task = makeTask({
      status: "completed",
      completed_at: "2026-06-08T08:04:00Z",
      result: { pr_url: "https://github.com/acme/repo/pull/7" },
    });
    renderWithI18n(<PastRow task={task} issueId="issue-1" />);

    const link = screen.getByRole("link", { name: "View pull request" });
    expect(link).toHaveAttribute("href", "https://github.com/acme/repo/pull/7");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noreferrer noopener");
  });

  it("omits the View-PR link when the result has no pr_url", () => {
    const task = makeTask({
      status: "completed",
      completed_at: "2026-06-08T08:04:00Z",
      result: { summary: "done" },
    });
    renderWithI18n(<PastRow task={task} issueId="issue-1" />);

    expect(screen.queryByRole("link", { name: "View pull request" })).not.toBeInTheDocument();
  });

  it("ignores a non-http pr_url (defensive read)", () => {
    const task = makeTask({
      status: "completed",
      completed_at: "2026-06-08T08:04:00Z",
      result: { pr_url: "javascript:alert(1)" },
    });
    renderWithI18n(<PastRow task={task} issueId="issue-1" />);

    expect(screen.queryByRole("link", { name: "View pull request" })).not.toBeInTheDocument();
  });

  it("does not render the link for a non-completed task even if pr_url is present", () => {
    const task = makeTask({
      status: "failed",
      completed_at: "2026-06-08T08:04:00Z",
      result: { pr_url: "https://github.com/acme/repo/pull/7" },
    });
    renderWithI18n(<PastRow task={task} issueId="issue-1" />);

    expect(screen.queryByRole("link", { name: "View pull request" })).not.toBeInTheDocument();
  });
});
