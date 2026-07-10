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
  getIssue: vi.fn(),
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

// The live-run marker hooks — real defaults are null / {} (idle, matching
// qa-live-progress.tsx's own initialData), overridden per-test to simulate a
// run in progress. Mocking these (rather than using the real hooks against
// an empty query cache) lets the "running case" tests drive the exact
// runningCaseId without wiring a whole QAMarkerWatcher + task snapshot.
const qaLiveProgressMocks = vi.hoisted(() => ({
  useRunningTestCaseId: vi.fn((): string | null => null),
  useLiveCaseVerdicts: vi.fn((): Record<string, "pass" | "fail"> => ({})),
}));
vi.mock("./qa-live-progress", () => ({
  useRunningTestCaseId: qaLiveProgressMocks.useRunningTestCaseId,
  useLiveCaseVerdicts: qaLiveProgressMocks.useLiveCaseVerdicts,
}));

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
  preconditions: "",
  priority: "p2",
  modality: "",
  criterion_ref: "",
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
    apiMocks.getIssue.mockResolvedValue({ id: "issue-1", description: null });
    apiMocks.cancelTaskById.mockResolvedValue({ id: "task-99", status: "cancelled" });
    qaLiveProgressMocks.useRunningTestCaseId.mockReturnValue(null);
    qaLiveProgressMocks.useLiveCaseVerdicts.mockReturnValue({});
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

describe("TestCasesPanel — running case + fail expansion", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.getAgentTaskSnapshot.mockResolvedValue([]);
    apiMocks.getIssue.mockResolvedValue({ id: "issue-1", description: null });
    apiMocks.cancelTaskById.mockResolvedValue({ id: "task-99", status: "cancelled" });
    qaLiveProgressMocks.useRunningTestCaseId.mockReturnValue(null);
    qaLiveProgressMocks.useLiveCaseVerdicts.mockReturnValue({});
  });

  it("pulses the running row and shows the sticky 'Running case X of N' summary with a progress bar", async () => {
    apiMocks.getIssueTestCases.mockResolvedValue({
      test_cases: [automatedCase({ id: "tc-1", title: "Checkout — happy path" }), automatedCase({ id: "tc-2", title: "Checkout — declined card" })],
    });
    qaLiveProgressMocks.useRunningTestCaseId.mockReturnValue("tc-2");

    renderPanel();

    await screen.findByText("Checkout — declined card");
    // "Running case 2 of 2 — Checkout — declined card" (test_cases.running_line)
    // — one combined sticky-summary text node, matched by regex since it also
    // overlaps with the row's own (exact-text) title span.
    expect(screen.getByText(/Running case 2 of 2 — Checkout — declined card/)).toBeInTheDocument();
    expect(screen.getByText("RUNS")).toBeInTheDocument();
  });

  it("shows a generic running message when the live marker names a case not in the list", async () => {
    apiMocks.getIssueTestCases.mockResolvedValue({ test_cases: [automatedCase()] });
    qaLiveProgressMocks.useRunningTestCaseId.mockReturnValue("unknown-case-id");

    renderPanel();

    await screen.findByText("Checkout — happy path");
    expect(screen.getByText("Running QA…")).toBeInTheDocument();
    expect(screen.queryByText(/Running case/)).not.toBeInTheDocument();
  });

  it("opens a fail row automatically and shows the agent's WHY output; a pass row stays collapsed", async () => {
    apiMocks.getIssueTestCases.mockResolvedValue({
      test_cases: [
        automatedCase({
          id: "tc-fail",
          title: "Checkout — expired card",
          latest_run: {
            id: "run-1",
            status: "fail",
            run_source: "agent",
            created_at: "2026-01-01T00:00:00Z",
            output: "Expected error toast, got a silent 500.",
            trace_path: "",
          },
        }),
        automatedCase({
          id: "tc-pass",
          title: "Checkout — happy path",
          latest_run: {
            id: "run-2",
            status: "pass",
            run_source: "agent",
            created_at: "2026-01-01T00:00:00Z",
            output: "",
            trace_path: "",
          },
        }),
      ],
    });

    renderPanel();

    // Fail case sorts first (statusRank) and its WHY is visible without a click.
    await screen.findByText("Expected error toast, got a silent 500.");

    // The passing case's row is present but its detail body never renders —
    // there's no `output` to show and it isn't auto-opened.
    expect(screen.getByText("Checkout — happy path")).toBeInTheDocument();
  });
});

describe("TestCasesPanel — suggest-from-ticket card", () => {
  const longDescription =
    "As a customer I can apply a coupon at checkout; invalid coupons show an inline error and the total stays unchanged.";

  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.getAgentTaskSnapshot.mockResolvedValue([]);
    apiMocks.getIssueTestCases.mockResolvedValue({ test_cases: [] });
    apiMocks.getIssue.mockResolvedValue({ id: "issue-1", description: longDescription });
    apiMocks.generateTestCases.mockResolvedValue({});
    qaLiveProgressMocks.useRunningTestCaseId.mockReturnValue(null);
    qaLiveProgressMocks.useLiveCaseVerdicts.mockReturnValue({});
  });

  it("offers generate-from-ticket for a described issue with zero cases, firing gen_test_cases", async () => {
    renderPanel();

    const btn = await screen.findByRole("button", { name: /Generate from ticket/ });
    fireEvent.click(btn);

    await waitFor(() => expect(apiMocks.generateTestCases).toHaveBeenCalledTimes(1));
    expect(apiMocks.generateTestCases).toHaveBeenCalledWith("issue-1");
  });

  it("dismiss hides the card and falls back to the plain empty state", async () => {
    renderPanel();

    fireEvent.click(await screen.findByTitle("Dismiss"));

    expect(screen.queryByRole("button", { name: /Generate from ticket/ })).not.toBeInTheDocument();
    // The generic empty state (with its trailing period) takes over.
    expect(await screen.findByText("No test cases yet.")).toBeInTheDocument();
  });

  it("shows the plain empty state instead when the description is trivial", async () => {
    apiMocks.getIssue.mockResolvedValue({ id: "issue-1", description: "wip" });
    renderPanel();

    expect(await screen.findByText("No test cases yet.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Generate from ticket/ })).not.toBeInTheDocument();
  });

  it("never shows the card once the issue has cases", async () => {
    apiMocks.getIssueTestCases.mockResolvedValue({ test_cases: [automatedCase()] });
    renderPanel();

    await screen.findByText("Checkout — happy path");
    expect(screen.queryByRole("button", { name: /Generate from ticket/ })).not.toBeInTheDocument();
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
