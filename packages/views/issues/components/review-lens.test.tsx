import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { ReviewLensBody } from "./review-lens";

// ReviewLensBody v2 — "agent reviews, human approves"
// (docs/review-stage-plan.md). Covers: the verdict card off
// api.getReviewVerdict, the empty state + Run review dispatch, the findings
// list, the human decision bar (approve confirm → api.reviewDecision;
// request_changes requires a note), the stale hint, and the v1 surface it
// keeps (gate cards, merge:override badge, deploy pointer).

const apiMocks = vi.hoisted(() => ({
  getIssue: vi.fn(),
  mergeReadiness: vi.fn(),
  getReviewVerdict: vi.fn(),
  reviewDecision: vi.fn(),
  sliceAction: vi.fn(),
  listIssuePullRequests: vi.fn(),
}));
const toastMocks = vi.hoisted(() => ({
  success: vi.fn(),
  error: vi.fn(),
  message: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("@agora/core", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("sonner", () => ({ toast: toastMocks }));
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
// ActorAvatar needs the workspace actor stores — identity is not under test.
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <span data-testid="actor-avatar" data-actor-id={actorId} />
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

const NONE_VERDICT = {
  verdict: "none",
  summary: "",
  commit_sha: "",
  files_reviewed: 0,
  findings: [],
  comment_id: "",
  reviewed_at: "",
  reviewer_agent_id: "",
};

function passVerdict(over: Record<string, unknown> = {}) {
  return {
    verdict: "pass",
    summary: "Clean change, conventions followed.",
    commit_sha: "deadbeefcafe1234",
    files_reviewed: 7,
    findings: [],
    comment_id: "c1",
    reviewed_at: "2026-07-12T10:00:00Z",
    reviewer_agent_id: "agent-9",
    ...over,
  };
}

function readyReadiness(over: Record<string, unknown> = {}) {
  return {
    ready: true,
    tier: "full",
    gates: [
      { name: "ci", status: "pass" },
      { name: "qa", status: "pass" },
      { name: "review", status: "pass" },
    ],
    reviews: [],
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
    apiMocks.getReviewVerdict.mockResolvedValue(NONE_VERDICT);
    apiMocks.listIssuePullRequests.mockResolvedValue({ pull_requests: [] });
    apiMocks.mergeReadiness.mockResolvedValue({
      ready: false,
      tier: "trivial",
      gates: [],
      reviews: [],
    });
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
    renderLens();

    const link = await screen.findByText("Open sprint deploys");
    expect(link.closest("a")).toHaveAttribute("href", "/acme/qa");
  });

  it("shows the merge:override badge when the issue carries the label", async () => {
    apiMocks.getIssue.mockResolvedValue(
      baseIssue({ labels: [{ id: "l1", name: "merge:override" }] }),
    );
    renderLens();

    await screen.findByText("Merge override");
  });

  it("renders the empty state when merge-readiness has no gates yet", async () => {
    renderLens();

    await screen.findByText("No merge-readiness gates yet.");
  });

  it("shows the 'No review yet' empty state and dispatches run_review from its button", async () => {
    apiMocks.sliceAction.mockResolvedValue({ kind: "run_review" });
    renderLens();

    await screen.findByText("No review yet");
    fireEvent.click(screen.getByRole("button", { name: "Run review" }));
    await waitFor(() =>
      expect(apiMocks.sliceAction).toHaveBeenCalledWith("issue-1", { kind: "run_review" }),
    );
    expect(toastMocks.success).toHaveBeenCalled();
  });

  it("renders the verdict card: verdict, summary, reviewer, commit, files reviewed", async () => {
    apiMocks.getReviewVerdict.mockResolvedValue(
      passVerdict({
        findings: [
          { file: "a.ts", line: 3, severity: "minor", title: "nit", detail: "" },
        ],
      }),
    );
    renderLens();

    await screen.findByText("Clean change, conventions followed.");
    expect(screen.getAllByText("Passed").length).toBeGreaterThan(0);
    expect(screen.getByTestId("actor-avatar")).toHaveAttribute("data-actor-id", "agent-9");
    expect(screen.getByText("deadbee")).toBeInTheDocument();
    expect(screen.getByText("7 files reviewed")).toBeInTheDocument();
    expect(screen.getByText("1 × Minor")).toBeInTheDocument();
    // With a verdict present the dispatch button flips to re-run.
    expect(screen.getByRole("button", { name: "Re-run review" })).toBeInTheDocument();
  });

  it("renders findings with severity, file:line, and expandable detail (blockers first)", async () => {
    apiMocks.getReviewVerdict.mockResolvedValue(
      passVerdict({
        verdict: "fail",
        summary: "1 blocker",
        findings: [
          { file: "docs/x.md", line: null, severity: "minor", title: "typo", detail: "" },
          {
            file: "server/auth.go",
            line: 42,
            severity: "blocker",
            title: "token compared with ==",
            detail: "Use constant-time compare.",
          },
        ],
      }),
    );
    renderLens();

    await screen.findByText("token compared with ==");
    const rows = screen.getAllByTestId("review-finding");
    expect(rows).toHaveLength(2);
    // Sorted blocker-first even though the payload lists minor first.
    expect(rows[0]).toHaveTextContent("Blocker");
    expect(rows[0]).toHaveTextContent("server/auth.go:42");
    expect(rows[0]).toHaveTextContent("Use constant-time compare.");
    expect(rows[1]).toHaveTextContent("Minor");
    expect(rows[1]).toHaveTextContent("docs/x.md");
  });

  it("approves through the confirm panel and fires api.reviewDecision", async () => {
    apiMocks.getReviewVerdict.mockResolvedValue(passVerdict());
    apiMocks.mergeReadiness.mockResolvedValue(readyReadiness());
    apiMocks.reviewDecision.mockResolvedValue({ action: "approve", merged_dispatch: true });
    renderLens();

    const approveBtn = await screen.findByRole("button", { name: "Approve & merge" });
    await waitFor(() => expect(approveBtn).toBeEnabled());
    fireEvent.click(approveBtn);

    // Confirm panel lists the gates, then the actual dispatch.
    await screen.findByText("Dispatch the merge order to the squad lead?");
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() =>
      expect(apiMocks.reviewDecision).toHaveBeenCalledWith("issue-1", { action: "approve" }),
    );
    expect(toastMocks.success).toHaveBeenCalled();
  });

  it("disables Approve & merge while merge-readiness is not ready", async () => {
    apiMocks.getReviewVerdict.mockResolvedValue(passVerdict());
    apiMocks.mergeReadiness.mockResolvedValue(
      readyReadiness({
        ready: false,
        gates: [
          { name: "ci", status: "fail" },
          { name: "qa", status: "pass" },
        ],
      }),
    );
    renderLens();

    const approveBtn = await screen.findByRole("button", { name: "Approve & merge" });
    expect(approveBtn).toBeDisabled();
  });

  it("enables Approve & merge with merge:override even when readiness is not ready", async () => {
    // merge:override is meant to bypass red gates, so the button must stay
    // enabled regardless of readiness.ready / verdict.
    apiMocks.getIssue.mockResolvedValue(
      baseIssue({ labels: [{ id: "l1", name: "merge:override" }] }),
    );
    apiMocks.getReviewVerdict.mockResolvedValue(passVerdict({ verdict: "fail" }));
    apiMocks.mergeReadiness.mockResolvedValue(
      readyReadiness({
        ready: false,
        gates: [{ name: "ci", status: "fail" }],
      }),
    );
    renderLens();

    const approveBtn = await screen.findByRole("button", { name: "Approve & merge" });
    await waitFor(() => expect(approveBtn).toBeEnabled());
  });

  it("toasts the merge-by-hand variant when approve returns merged_dispatch:false", async () => {
    apiMocks.getReviewVerdict.mockResolvedValue(passVerdict());
    apiMocks.mergeReadiness.mockResolvedValue(readyReadiness());
    // No squad lead resolved on the backend — nothing was dispatched.
    apiMocks.reviewDecision.mockResolvedValue({ action: "approve", merged_dispatch: false });
    renderLens();

    const approveBtn = await screen.findByRole("button", { name: "Approve & merge" });
    await waitFor(() => expect(approveBtn).toBeEnabled());
    fireEvent.click(approveBtn);

    await screen.findByText("Dispatch the merge order to the squad lead?");
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() =>
      expect(apiMocks.reviewDecision).toHaveBeenCalledWith("issue-1", { action: "approve" }),
    );
    expect(toastMocks.success).toHaveBeenCalledWith(
      "Approved — merge it by hand (no squad lead to dispatch to)",
    );
  });

  it("request changes requires a note, then fires api.reviewDecision with it", async () => {
    apiMocks.getReviewVerdict.mockResolvedValue(passVerdict({ verdict: "fail" }));
    apiMocks.reviewDecision.mockResolvedValue({
      action: "request_changes",
      status: "in_progress",
      dispatched: true,
    });
    renderLens();

    fireEvent.click(await screen.findByRole("button", { name: "Request changes" }));
    const submit = await screen.findByRole("button", { name: "Send back to author" });

    // Empty note is rejected client-side — the API must not be called.
    fireEvent.click(submit);
    expect(apiMocks.reviewDecision).not.toHaveBeenCalled();
    expect(toastMocks.error).toHaveBeenCalled();

    fireEvent.change(screen.getByPlaceholderText("What must change before this can merge?"), {
      target: { value: "Fix the auth check" },
    });
    fireEvent.click(submit);
    await waitFor(() =>
      expect(apiMocks.reviewDecision).toHaveBeenCalledWith("issue-1", {
        action: "request_changes",
        note: "Fix the auth check",
      }),
    );
  });

  it("shows the stale hint when an open PR was updated after the review", async () => {
    apiMocks.getReviewVerdict.mockResolvedValue(
      passVerdict({ reviewed_at: "2026-07-12T10:00:00Z" }),
    );
    apiMocks.listIssuePullRequests.mockResolvedValue({
      pull_requests: [
        {
          id: "pr1",
          state: "open",
          pr_updated_at: "2026-07-12T11:00:00Z",
          pr_created_at: "2026-07-12T09:00:00Z",
          title: "t",
          repo_owner: "o",
          repo_name: "r",
          number: 1,
          html_url: "https://example.com",
          branch: null,
          author_login: null,
          author_avatar_url: null,
          merged_at: null,
          closed_at: null,
          workspace_id: "ws-1",
        },
      ],
    });
    renderLens();

    await screen.findByText("The PR changed after this review — re-run it before approving.");
  });

  it("does not show the stale hint when the review postdates the PR's last update", async () => {
    apiMocks.getReviewVerdict.mockResolvedValue(
      passVerdict({ reviewed_at: "2026-07-12T12:00:00Z" }),
    );
    apiMocks.listIssuePullRequests.mockResolvedValue({
      pull_requests: [
        {
          id: "pr1",
          state: "open",
          pr_updated_at: "2026-07-12T11:00:00Z",
          pr_created_at: "2026-07-12T09:00:00Z",
          title: "t",
          repo_owner: "o",
          repo_name: "r",
          number: 1,
          html_url: "https://example.com",
          branch: null,
          author_login: null,
          author_avatar_url: null,
          merged_at: null,
          closed_at: null,
          workspace_id: "ws-1",
        },
      ],
    });
    renderLens();

    await screen.findByText("Clean change, conventions followed.");
    expect(
      screen.queryByText("The PR changed after this review — re-run it before approving."),
    ).not.toBeInTheDocument();
  });
});
