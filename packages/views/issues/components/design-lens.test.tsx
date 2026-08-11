import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { DesignLensBody } from "./design-lens";

// DesignLensBody is a thin re-mount of the existing design sections
// (docs/sdlc-stage-cockpit-plan.md, phase E). Each section already
// self-gates (renders null when not relevant), so this test only covers the
// lens's OWN composition: which query-derived signal flips it from the
// empty state to the sections wrapper. Children are stubbed — they have no
// logic of their own left to re-test here.

const apiMocks = vi.hoisted(() => ({
  getIssue: vi.fn(),
  listTimeline: vi.fn(),
  getQAEvidence: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("@agora/core", () => ({ useWorkspaceId: () => "ws-1" }));

vi.mock("./figma-links-section", () => ({
  FigmaLinksSection: () => <div data-testid="figma-links" />,
}));
vi.mock("./design-proposal-section", () => ({
  DesignProposalSection: () => <div data-testid="design-proposal" />,
}));
vi.mock("./design-audit-section", () => ({
  DesignAuditSection: () => <div data-testid="design-audit" />,
}));
vi.mock("./design-context-review-section", () => ({
  DesignContextReviewSection: () => <div data-testid="design-context-review" />,
}));
vi.mock("./design-screenshot-compare", () => ({
  DesignScreenshotCompare: () => <div data-testid="design-screenshot-compare" />,
}));
vi.mock("../../qa/components/qa-design-compare", () => ({
  QADesignCompare: () => <div data-testid="qa-design-compare" />,
}));

function baseIssue(over: Record<string, unknown> = {}) {
  return {
    id: "issue-1",
    workspace_id: "ws-1",
    number: 1,
    identifier: "MUL-1",
    title: "Fix onboarding",
    description: null,
    status: "in_progress",
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
    verdict: "pending",
    source: "agent",
    summary: "",
    result: { verdict: "pending", summary: "", commands: [], design: null },
    captured_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

function renderLens() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={qc}>
        <DesignLensBody issueId="issue-1" />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("DesignLensBody", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.listTimeline.mockResolvedValue([]);
    apiMocks.getQAEvidence.mockResolvedValue(baseEvidence());
  });

  it("renders the empty state when the issue has no design signals", async () => {
    apiMocks.getIssue.mockResolvedValue(baseIssue());
    renderLens();

    await screen.findByText("No design references on this issue.");
    expect(screen.queryByTestId("figma-links")).not.toBeInTheDocument();
    expect(screen.queryByTestId("design-proposal")).not.toBeInTheDocument();
  });

  it("renders the design workbench (primary screenshot compare + right-column sections) when the issue links a Figma design", async () => {
    apiMocks.getIssue.mockResolvedValue(
      baseIssue({ description: "See https://figma.com/file/abcdefghijklmnop/My-Design" }),
    );
    renderLens();

    await screen.findByTestId("figma-links");
    expect(screen.getByTestId("design-screenshot-compare")).toBeInTheDocument();
    expect(screen.getByTestId("design-proposal")).toBeInTheDocument();
    expect(screen.getByTestId("design-audit")).toBeInTheDocument();
    expect(screen.getByTestId("qa-design-compare")).toBeInTheDocument();
    expect(screen.queryByText("No design references on this issue.")).not.toBeInTheDocument();
  });

  it("renders the design sections when the run_qa verdict carries a design result", async () => {
    apiMocks.getIssue.mockResolvedValue(baseIssue());
    apiMocks.getQAEvidence.mockResolvedValue(
      baseEvidence({
        result: {
          verdict: "pass",
          summary: "",
          commands: [],
          design: { verdict: "pass", reference_node: "1:2", mismatches: [], lint: [] },
        },
      }),
    );
    renderLens();

    await screen.findByTestId("qa-design-compare");
  });

  it("opens the Design lens for a pending Design context proposal", async () => {
    apiMocks.getIssue.mockResolvedValue(baseIssue());
    apiMocks.listTimeline.mockResolvedValue([{ type: "comment", id: "c1", actor_type: "agent", actor_id: "a1", content: "<!-- design-context-proposal -->", created_at: "" }]);
    renderLens();

    await screen.findByTestId("design-context-review");
  });
});
