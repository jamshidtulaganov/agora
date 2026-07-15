import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { QAEvidenceSection } from "./qa-evidence-section";

const apiMocks = vi.hoisted(() => ({
  getQAEvidence: vi.fn(),
  getIssueTestCases: vi.fn(),
  sliceAction: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock("../../qa/components/qa-design-compare", () => ({ QADesignCompare: () => null }));

function renderSection() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
        <QAEvidenceSection issueId="issue-1" status="in_review" />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

function testCase(over: Record<string, unknown>) {
  return {
    id: "case-1",
    issue_id: "issue-1",
    title: "Greeting appears after submit",
    steps: "Open the app and submit a name.",
    expected: "A greeting containing the submitted name is visible.",
    kind: "automated",
    source: "agent",
    author_type: "agent",
    category: "positive",
    preconditions: "The app is running.",
    priority: "p1",
    modality: "ui",
    criterion_ref: "AC1",
    created_at: "2026-07-15T00:00:00Z",
    latest_run: null,
    ...over,
  };
}

beforeEach(() => {
  apiMocks.getQAEvidence.mockReset();
  apiMocks.getIssueTestCases.mockReset();
  apiMocks.sliceAction.mockReset();
});

describe("QAEvidenceSection", () => {
  it("leads with named behaviors and expected/observed failure details", async () => {
    apiMocks.getQAEvidence.mockResolvedValue({
      id: "evidence-1",
      issue_id: "issue-1",
      verdict: "fail",
      summary: "docs/orchestration-worktree-smoke.md not found on any branch",
      result: {
        verdict: "fail",
        summary: "",
        commands: [
          {
            cmd: "node /tmp/case-11df7aa2.mjs",
            baseline_exit: null,
            branch_exit: 0,
            kind: "pass",
            error: "",
          },
          {
            cmd: "check: docs/orchestration-worktree-smoke.md exists with exact content",
            baseline_exit: null,
            branch_exit: 1,
            kind: "new_failure",
            error: "file not found on any branch — implementation not pushed",
          },
        ],
        screenshots: [],
      },
      captured_at: "2026-07-15T10:34:44Z",
      reconciled_state: "fail",
      commit_sha: "",
      triggered_by: "agent",
      started_at: "",
      finished_at: "",
    });
    apiMocks.getIssueTestCases.mockResolvedValue({
      test_cases: [
        testCase({
          latest_run: {
            id: "run-1",
            status: "fail",
            run_source: "agent",
            created_at: "2026-07-15T10:34:44Z",
            output: "Expected a greeting, but the page stayed empty.",
            trace_path: "",
          },
        }),
        testCase({
          id: "case-2",
          title: "Page opens without errors",
          expected: "The page loads successfully.",
          latest_run: {
            id: "run-2",
            status: "pass",
            run_source: "agent",
            created_at: "2026-07-15T10:34:44Z",
            output: "",
            trace_path: "",
          },
        }),
      ],
    });

    renderSection();

    expect(await screen.findByText("QA report")).toBeInTheDocument();
    expect(screen.getByText("At least one expected behavior did not match the tested result.")).toBeInTheDocument();
    expect(screen.getByRole("button", { expanded: true })).not.toHaveTextContent("not found on any branch");
    expect(screen.getByText("What QA checked")).toBeInTheDocument();
    expect(screen.getByText("Greeting appears after submit")).toBeInTheDocument();
    expect(screen.getByText("A greeting containing the submitted name is visible.")).toBeInTheDocument();
    expect(screen.getByText("Expected a greeting, but the page stayed empty.")).toBeInTheDocument();
    expect(screen.getByText("Page opens without errors")).toBeInTheDocument();
    expect(screen.getByText("Required file has the expected content")).toBeInTheDocument();
    expect(screen.getByText("The required file was not present in the tested result.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Run checks again" })).toBeInTheDocument();

    const technicalCommand = screen.getByText("check: docs/orchestration-worktree-smoke.md exists with exact content");
    expect(technicalCommand.closest("details")).not.toHaveAttribute("open");
  });

  it("makes missing requirement-based cases explicit", async () => {
    apiMocks.getQAEvidence.mockResolvedValue({
      id: "evidence-1",
      issue_id: "issue-1",
      verdict: "pass",
      summary: "All checks passed",
      result: { verdict: "pass", summary: "", commands: [], screenshots: [] },
      captured_at: "2026-07-15T10:34:44Z",
      reconciled_state: "pass",
      commit_sha: "",
      triggered_by: "agent",
      started_at: "",
      finished_at: "",
    });
    apiMocks.getIssueTestCases.mockResolvedValue({ test_cases: [] });

    renderSection();

    expect(await screen.findByText("No named test cases were recorded")).toBeInTheDocument();
    expect(screen.getByText(/not linked to requirement-based test cases/i)).toBeInTheDocument();
  });
});
