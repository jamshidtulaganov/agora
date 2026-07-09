import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { QALensBody } from "./qa-lens";

// QALensBody is the QA lens re-homed into the issue cockpit
// (docs/sdlc-stage-cockpit-plan.md, phase D). These tests cover its OWN
// composition — the verdict block and the triage mutations (setVerdict /
// sendBack) — not the already-tested child instruments (live bay, test
// cases, PR list, design compare), which are stubbed out here so this file
// stays a focused unit test of the lens itself.

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

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("@agora/core", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

// Stub the child instruments — each already has its own coverage elsewhere
// (test-cases-panel.test.tsx, qa-suite-view.test.tsx). Stubbing keeps this
// file from having to mock their entire transitive query graph.
vi.mock("./qa-live-browser", () => ({ QALiveBrowser: () => null }));
vi.mock("./qa-live-progress", () => ({ QALiveProgress: () => null }));
vi.mock("./test-cases-panel", () => ({ TestCasesPanel: () => null }));
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
  });

  it("renders the verdict and triage actions from mocked issue + evidence", async () => {
    renderLens();

    await screen.findByText("Passed"); // qa_evidence.verdict_pass
    expect(screen.getByRole("button", { name: "Pass" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Fail" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Send back to dev" })).toBeInTheDocument();
    expect(apiMocks.getIssue).toHaveBeenCalledWith("issue-1");
    expect(apiMocks.getQAEvidence).toHaveBeenCalledWith("issue-1");
  });

  it("Pass button attaches the qa:pass label", async () => {
    renderLens();

    const passBtn = await screen.findByRole("button", { name: "Pass" });
    fireEvent.click(passBtn);

    await waitFor(() => expect(apiMocks.attachLabel).toHaveBeenCalledWith("issue-1", "label-pass"));
    expect(apiMocks.detachLabel).not.toHaveBeenCalled();
  });

  it("send-back posts the QA note as a comment, marks qa:fail, and moves the issue to in_progress", async () => {
    renderLens();

    const noteInput = await screen.findByLabelText("QA note (optional)");
    fireEvent.change(noteInput, { target: { value: "Repro: click X, see Y" } });

    fireEvent.click(screen.getByRole("button", { name: "Send back to dev" }));

    await waitFor(() =>
      expect(apiMocks.updateIssue).toHaveBeenCalledWith("issue-1", { status: "in_progress" }),
    );
    expect(apiMocks.attachLabel).toHaveBeenCalledWith("issue-1", "label-fail");
    expect(apiMocks.createComment).toHaveBeenCalledWith("issue-1", "Repro: click X, see Y");
  });
});
