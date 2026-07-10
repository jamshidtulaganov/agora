import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import type { TestCase } from "@agora/core/types";
import enIssues from "../../locales/en/issues.json";
import { TestCasesPanel } from "./test-cases-panel";
import { QAIssueRow } from "./qa-lane";

// The STOP affordance for a running QA / test-case run. The task id to cancel is
// resolved from the agent task snapshot (the running row whose issue_id matches),
// because the slice-action dispatch response carries no task id.

const apiMocks = vi.hoisted(() => ({
  getIssueTestCases: vi.fn(),
  getAgentTaskSnapshot: vi.fn(),
  cancelTaskById: vi.fn(),
  generateTestCases: vi.fn(),
  runTestCases: vi.fn(),
  recordTestCaseRun: vi.fn(),
  sliceAction: vi.fn(),
  launchTrace: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("sonner", () => ({ toast: Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn() }) }));

// Keep the snapshot query focused on the mocked api, without pulling the whole
// @agora/core/agents barrel into jsdom.
vi.mock("@agora/core/agents", () => ({
  agentTaskSnapshotKeys: {
    list: (wsId: string) => ["workspaces", wsId, "agent-task-snapshot", "list"] as const,
  },
  agentTaskSnapshotOptions: (wsId: string) => ({
    queryKey: ["workspaces", wsId, "agent-task-snapshot", "list"] as const,
    queryFn: () => apiMocks.getAgentTaskSnapshot(),
  }),
}));

const automatedCase = (over: Partial<TestCase> = {}): TestCase => ({
  id: "tc-1",
  issue_id: "issue-1",
  title: "Checkout — happy path",
  steps: "1. add to cart\n2. pay",
  expected: "order confirmed",
  kind: "automated",
  source: "human",
  author_type: "member",
  category: "positive",
  created_at: "",
  latest_run: null,
  ...over,
});

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={qc}>
        <TestCasesPanel issueId="issue-1" />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("TestCasesPanel stop affordance", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.getIssueTestCases.mockResolvedValue({ test_cases: [automatedCase()] });
    apiMocks.cancelTaskById.mockResolvedValue({ id: "task-99", status: "cancelled" });
  });

  it("cancels the issue's running task id (resolved from the snapshot) when Stop is clicked", async () => {
    // A running QA task for THIS issue — its `.id` is what cancel needs. The
    // second row (different issue, also running) must be ignored.
    apiMocks.getAgentTaskSnapshot.mockResolvedValue([
      { id: "task-other", issue_id: "issue-2", status: "running" },
      { id: "task-99", issue_id: "issue-1", status: "running" },
    ]);

    renderPanel();

    const stopBtn = await screen.findByTitle("Stop the run");
    fireEvent.click(stopBtn);

    await waitFor(() => expect(apiMocks.cancelTaskById).toHaveBeenCalledTimes(1));
    expect(apiMocks.cancelTaskById).toHaveBeenCalledWith("task-99");
  });

  it("hides the Stop button when no task is running for the issue", async () => {
    apiMocks.getAgentTaskSnapshot.mockResolvedValue([
      { id: "task-done", issue_id: "issue-1", status: "completed" },
    ]);

    renderPanel();

    // Wait for the case to render so the snapshot query has settled too.
    await screen.findByText("Checkout — happy path");
    expect(screen.queryByTitle("Stop the run")).not.toBeInTheDocument();
    expect(apiMocks.cancelTaskById).not.toHaveBeenCalled();
  });
});

describe("QAIssueRow stop affordance (queue)", () => {
  it("renders a Stop button on a running row and calls onStopRun with the resolved task id", () => {
    const onStopRun = vi.fn();
    render(
      <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
        <QAIssueRow
          issue={{ id: "issue-1", identifier: "MUL-1", title: "T", priority: "none", labels: [] } as never}
          isLive
          runningTaskId="task-99"
          onStopRun={onStopRun}
        />
      </I18nProvider>,
    );

    const stopBtn = screen.getByRole("button", { name: "Stop the running QA gate" });
    fireEvent.click(stopBtn);

    expect(onStopRun).toHaveBeenCalledTimes(1);
    expect(onStopRun).toHaveBeenCalledWith("task-99");
  });

  it("does not render a Stop button when the row is not live", () => {
    render(
      <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
        <QAIssueRow
          issue={{ id: "issue-1", identifier: "MUL-1", title: "T", priority: "none", labels: [] } as never}
          isLive={false}
          runningTaskId={null}
          onStopRun={vi.fn()}
        />
      </I18nProvider>,
    );
    expect(screen.queryByRole("button", { name: "Stop the running QA gate" })).not.toBeInTheDocument();
  });
});
