import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { ReviewLensBody } from "./review-lens";

// ReviewLensBody is a read-only rollup of merge readiness + the PR list
// (docs/sdlc-stage-cockpit-plan.md, phase E). v1 has no merge/override
// mutations, so this test only covers rendering: gate cards from
// api.mergeReadiness, the merge:override badge, and the empty state.

const apiMocks = vi.hoisted(() => ({
  getIssue: vi.fn(),
  mergeReadiness: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("@agora/core", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@agora/core/paths", () => ({
  useWorkspacePaths: () => ({ qa: () => "/acme/qa" }),
}));
// AppLink pulls in NavigationProvider context (useNavigation) — stub it as a
// plain anchor so the deploy-readiness pointer renders without a full
// NavigationProvider tree, mirroring comment-card.test.tsx's approach.
vi.mock("../../navigation", () => ({
  AppLink: ({ href, children, className }: { href: string; children: ReactNode; className?: string }) => (
    <a href={href} className={className}>
      {children}
    </a>
  ),
}));
vi.mock("./pull-request-list", () => ({
  PullRequestList: () => <div data-testid="pull-request-list" />,
}));

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

function renderLens() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={qc}>
        <ReviewLensBody issueId="issue-1" />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("ReviewLensBody", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.getIssue.mockResolvedValue(baseIssue());
  });

  it("renders a gate card per gate with its pass/fail/pending state and the tier", async () => {
    apiMocks.mergeReadiness.mockResolvedValue({
      ready: false,
      tier: "light",
      gates: [
        { name: "ci", status: "pass" },
        { name: "qa", status: "fail" },
      ],
      blocked: ["qa"],
      reviews: [],
    });
    renderLens();

    await screen.findByText("ci");
    expect(screen.getByText("qa")).toBeInTheDocument();
    expect(screen.getByText("Passed")).toBeInTheDocument();
    expect(screen.getByText("Failed")).toBeInTheDocument();
    expect(screen.getByText(/light/)).toBeInTheDocument();
    expect(screen.getByText(/Blocked/)).toBeInTheDocument();
    expect(screen.getByTestId("pull-request-list")).toBeInTheDocument();
  });

  it("points to the QA cockpit's sprint view for deploy status", async () => {
    apiMocks.mergeReadiness.mockResolvedValue({
      ready: true,
      tier: "trivial",
      gates: [{ name: "ci", status: "pass" }],
      reviews: [],
    });
    renderLens();

    const link = await screen.findByText("Open sprint deploys");
    expect(link.closest("a")).toHaveAttribute("href", "/acme/qa");
  });

  it("shows the merge:override badge when the issue carries the label", async () => {
    apiMocks.getIssue.mockResolvedValue(
      baseIssue({ labels: [{ id: "l1", name: "merge:override" }] }),
    );
    apiMocks.mergeReadiness.mockResolvedValue({
      ready: true,
      tier: "trivial",
      gates: [{ name: "ci", status: "pass" }],
      reviews: [],
    });
    renderLens();

    await screen.findByText("Merge override");
  });

  it("renders the empty state when merge-readiness has no gates yet", async () => {
    apiMocks.mergeReadiness.mockResolvedValue({
      ready: false,
      tier: "trivial",
      gates: [],
      reviews: [],
    });
    renderLens();

    await screen.findByText("No merge-readiness gates yet.");
  });
});
