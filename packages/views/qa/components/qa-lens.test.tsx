import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { QALensBody } from "./qa-lens";

// QALensBody is the QA lens re-homed into the issue cockpit
// (docs/sdlc-stage-cockpit-plan.md, phase D). These tests cover its OWN
// composition — the verdict block, the triage mutations (setVerdict /
// sendBack), and the signal-driven live bay (open/idle state, driven by
// useQaRunningTasks) — not the already-tested child instruments (test cases,
// PR list, design compare), which are stubbed out here so this file stays a
// focused unit test of the lens itself.
//
// QALiveBrowser is stubbed with a light active/idle marker (instead of the
// usual `() => null`) so these tests can assert on the lens's OWN
// active/onOpen/onCollapse wiring without re-testing QALiveBrowser's actual
// pane rendering (a separate concern, out of scope here).

const apiMocks = vi.hoisted(() => ({
  getIssue: vi.fn(),
  getQAEvidence: vi.fn(),
  listLabels: vi.fn(),
  attachLabel: vi.fn(),
  detachLabel: vi.fn(),
  createComment: vi.fn(),
  updateIssue: vi.fn(),
  sliceAction: vi.fn(),
  runIssueSprintRegression: vi.fn(),
}));

const qaLiveProgressMocks = vi.hoisted(() => ({
  useQaRunningTasks: vi.fn((): { id: string }[] => []),
}));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("@agora/core", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

// Stub the child instruments — each already has its own coverage elsewhere
// (test-cases-panel.test.tsx, qa-suite-view.test.tsx). Stubbing keeps this
// file from having to mock their entire transitive query graph.
vi.mock("./qa-live-browser", () => ({
  QALiveBrowser: ({
    active,
    onOpen,
    onCollapse,
  }: {
    active: boolean;
    running: boolean;
    onOpen: () => void;
    onCollapse: () => void;
  }) =>
    active ? (
      <div data-testid="live-bay-active">
        <button type="button" onClick={onCollapse}>
          collapse
        </button>
      </div>
    ) : (
      <div data-testid="live-bay-idle">
        <button type="button" onClick={onOpen}>
          Open live testing
        </button>
      </div>
    ),
}));
vi.mock("./qa-live-progress", () => ({
  QALiveProgress: () => null,
  useQaRunningTasks: qaLiveProgressMocks.useQaRunningTasks,
}));
vi.mock("./test-cases-panel", () => ({ TestCasesPanel: () => null }));
vi.mock("./qa-activity-panel", () => ({ QAActivityPanel: () => null }));
vi.mock("./qa-design-compare", () => ({ QADesignCompare: () => null }));
vi.mock("./file-bug-sheet", () => ({ FileBugSheet: () => null }));
vi.mock("../../issues/components/pull-request-list", () => ({ PullRequestList: () => null }));
vi.mock("../../issues/components/qa-result", () => ({ StructuredResult: () => null }));

function baseIssue(over: Record<string, unknown> = {}) {
  return {
    id: "issue-1",
    workspace_id: "ws-1",
    number: 1,
    identifier: "MUL-1",
    title: "Fix onboarding",
    description: null,
    status: "in_review",
    priority: "none",
    assignee_type: null,
    assignee_id: null,
    creator_type: "member",
    creator_id: "u1",
    parent_issue_id: null,
    project_id: "project-1",
    position: 0,
    start_date: null,
    due_date: null,
    metadata: {},
    labels: [] as { id: string; name: string }[],
    created_at: "",
    updated_at: "",
    ...over,
  };
}

function baseEvidence(over: Record<string, unknown> = {}) {
  return {
    id: "ev-1",
    issue_id: "issue-1",
    baseline_ref: "",
    branch_sha: "",
    verdict: "pass",
    source: "agent",
    summary: "All checks passed",
    result: { verdict: "pass", summary: "", commands: [], design: null },
    captured_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

function renderLens() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={qc}>
        <QALensBody issueId="issue-1" />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("QALensBody", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.getIssue.mockResolvedValue(baseIssue());
    apiMocks.getQAEvidence.mockResolvedValue(baseEvidence());
    apiMocks.listLabels.mockResolvedValue({
      labels: [
        { id: "label-pass", name: "qa:pass" },
        { id: "label-fail", name: "qa:fail" },
      ],
    });
    apiMocks.attachLabel.mockResolvedValue(undefined);
    apiMocks.detachLabel.mockResolvedValue(undefined);
    apiMocks.createComment.mockResolvedValue(undefined);
    apiMocks.updateIssue.mockResolvedValue(undefined);
    // Default: no live QA run — the bay starts idle. Individual tests
    // override this to simulate a running QA-squad task.
    qaLiveProgressMocks.useQaRunningTasks.mockReturnValue([]);
  });

  it("renders the verdict chip (state + source) and the primary triage actions from mocked issue + evidence", async () => {
    renderLens();

    await screen.findByText("Passed"); // qa_evidence.verdict_pass
    expect(screen.getByText("agent")).toBeInTheDocument(); // qa_review.source_agent — no human override yet
    expect(screen.getByRole("button", { name: "Override" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Send back to dev" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Re-run QA" })).toBeInTheDocument();
    // Pass/Fail buttons are gone — the chip + Override dropdown replace them.
    expect(screen.queryByRole("button", { name: "Pass" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Fail" })).not.toBeInTheDocument();
    expect(apiMocks.getIssue).toHaveBeenCalledWith("issue-1");
    expect(apiMocks.getQAEvidence).toHaveBeenCalledWith("issue-1");
  });

  it("Override → Mark pass attaches the qa:pass label", async () => {
    renderLens();

    fireEvent.click(await screen.findByRole("button", { name: "Override" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Mark pass" }));

    await waitFor(() => expect(apiMocks.attachLabel).toHaveBeenCalledWith("issue-1", "label-pass"));
    expect(apiMocks.detachLabel).not.toHaveBeenCalled();
  });

  it("shows the chip as human-sourced when the qa:fail label diverges from the agent's own pass verdict", async () => {
    // A human already flipped the label to qa:fail while the agent's own
    // evidence (from the default beforeEach mock) still says "pass" — the
    // divergence IS the override signal (see isOverride in qa-lens.tsx).
    apiMocks.getIssue.mockResolvedValue(
      baseIssue({ labels: [{ id: "label-fail", name: "qa:fail" }] }),
    );
    renderLens();

    await screen.findByText("Failed"); // qa_evidence.verdict_fail — human override wins
    expect(screen.getByText("human")).toBeInTheDocument(); // qa_review.source_human
  });

  it("send-back opens a dialog; confirming posts the QA note as a comment, marks qa:fail, and moves the issue to in_progress", async () => {
    renderLens();

    fireEvent.click(await screen.findByRole("button", { name: "Send back to dev" }));

    const noteInput = await screen.findByLabelText("QA note (optional)");
    fireEvent.change(noteInput, { target: { value: "Repro: click X, see Y" } });

    fireEvent.click(screen.getByRole("button", { name: "Send back" }));

    await waitFor(() =>
      expect(apiMocks.updateIssue).toHaveBeenCalledWith("issue-1", { status: "in_progress" }),
    );
    expect(apiMocks.attachLabel).toHaveBeenCalledWith("issue-1", "label-fail");
    expect(apiMocks.createComment).toHaveBeenCalledWith("issue-1", "Repro: click X, see Y");
  });

  it("more-actions overflow menu fires the regression re-run", async () => {
    renderLens();

    fireEvent.click(await screen.findByRole("button", { name: "More actions" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Run sprint regression" }));

    await waitFor(() => expect(apiMocks.runIssueSprintRegression).toHaveBeenCalledWith("issue-1"));
  });

  describe("live bay", () => {
    it("starts idle: compact card only, no browser pane, review column present", async () => {
      renderLens();

      await screen.findByText("Passed"); // review column has loaded
      expect(await screen.findByTestId("live-bay-idle")).toBeInTheDocument();
      expect(screen.queryByTestId("live-bay-active")).not.toBeInTheDocument();
      // Review content (verdict + triage) is present alongside the idle card.
      expect(screen.getByRole("button", { name: "Override" })).toBeInTheDocument();
    });

    it("auto-opens the bay when a QA-squad task is running", async () => {
      qaLiveProgressMocks.useQaRunningTasks.mockReturnValue([{ id: "task-1" }]);
      renderLens();

      expect(await screen.findByTestId("live-bay-active")).toBeInTheDocument();
      expect(screen.queryByTestId("live-bay-idle")).not.toBeInTheDocument();
    });

    it('opens the bay when "Open live testing" is clicked, with no run active', async () => {
      renderLens();

      const openBtn = await screen.findByRole("button", { name: "Open live testing" });
      fireEvent.click(openBtn);

      expect(await screen.findByTestId("live-bay-active")).toBeInTheDocument();
      expect(screen.queryByTestId("live-bay-idle")).not.toBeInTheDocument();
    });
  });
});
