import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import enCommon from "../../locales/en/common.json";
import { DecisionQueue } from "./decision-queue";

// The decision queue (docs/orchestration-upgrade-plan.md §A2). What these
// tests pin:
//   - the SERVER's ranking (score desc) is the rendered order, never re-sorted;
//   - a kind this build has never heard of still renders and still opens its
//     issue, rather than being dropped or blanking the list;
//   - `risk_tier: ""` renders NO chip ("no opinion" is not "safe");
//   - the batch pass steps through the picked ids one at a time;
//   - a failed fetch never reads as "nothing is waiting on you".

const apiMocks = vi.hoisted(() => ({ getDecisionQueue: vi.fn() }));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("@agora/core", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@agora/core/paths", () => ({
  useWorkspacePaths: () => ({ issueDetail: (id: string) => `/w/issues/${id}` }),
}));
vi.mock("../../navigation", () => ({
  AppLink: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));
// The review workspace has its own transitive query graph; the pass only
// needs to prove WHICH issue it is showing.
vi.mock("../../issues/components/review-lens", () => ({
  WorkLensBody: ({ issueId }: { issueId: string }) => <div>review surface {issueId}</div>,
}));

function row(over: Record<string, unknown> = {}) {
  return {
    kind: "merge_ready",
    issue_id: "issue-1",
    identifier: "MUL-1",
    title: "Export invoices as PDF",
    status: "in_review",
    project_id: "proj-1",
    risk_tier: "guarded",
    risk_tier_source: "risk_map",
    since: "",
    age_hours: 1,
    needed: "",
    needed_code: "",
    score: 70,
    stale_reason: "",
    open_pr_count: 1,
    labels: [],
    ...over,
  };
}

function renderQueue() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues, common: enCommon } }}>
      <QueryClientProvider client={qc}>
        <DecisionQueue />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("DecisionQueue", () => {
  beforeEach(() => vi.clearAllMocks());

  it("renders the rows in the server's ranked order, with kind glyphs and needs lines", async () => {
    apiMocks.getDecisionQueue.mockResolvedValue({
      items: [
        row({
          kind: "escalation",
          issue_id: "issue-9",
          identifier: "MUL-9",
          title: "Invoice export",
          risk_tier: "critical",
          age_hours: 48,
        }),
        row(),
        row({ kind: "qa_failed", issue_id: "issue-3", identifier: "MUL-3", title: "Flaky login" }),
        row({ kind: "review_failed", issue_id: "issue-4", identifier: "MUL-4", title: "Bump deps" }),
      ],
      total: 4,
      counts: { escalation: 1, merge_ready: 1, qa_failed: 1, review_failed: 1 },
    });

    renderQueue();

    expect(await screen.findByText("Invoice export")).toBeInTheDocument();
    const identifiers = screen.getAllByText(/^MUL-\d$/).map((n) => n.textContent);
    expect(identifiers).toEqual(["MUL-9", "MUL-1", "MUL-3", "MUL-4"]);

    // One glyph per kind, labelled.
    expect(screen.getByLabelText("Question")).toBeInTheDocument();
    expect(screen.getByLabelText("Merge")).toBeInTheDocument();
    expect(screen.getByLabelText("QA")).toBeInTheDocument();
    expect(screen.getByLabelText("Review")).toBeInTheDocument();

    // Per-kind "what is needed" copy when the server sent none.
    expect(screen.getByText("An agent is waiting on your answer")).toBeInTheDocument();
    expect(screen.getByText("Checks are in — waiting on your approval")).toBeInTheDocument();
    expect(screen.getByText("A QA result needs your call")).toBeInTheDocument();
    expect(screen.getByText("Review found problems — needs your decision")).toBeInTheDocument();
  });

  it("states exact totals and the oldest age — never a rate", async () => {
    apiMocks.getDecisionQueue.mockResolvedValue({
      items: [row(), row({ issue_id: "issue-2", identifier: "MUL-2", title: "Another", age_hours: 48 })],
      total: 7,
      counts: { escalation: 0, merge_ready: 7, qa_failed: 0, review_failed: 0 },
    });

    renderQueue();

    expect(await screen.findByText(/7 waiting/)).toBeInTheDocument();
    expect(screen.getByText(/oldest 2d ago/)).toBeInTheDocument();
  });

  it("prefers the server's own needs sentence over the per-kind copy", async () => {
    apiMocks.getDecisionQueue.mockResolvedValue({
      items: [row({ needed: "2 blockers from code review" })],
      total: 1,
      counts: { escalation: 0, merge_ready: 1, qa_failed: 0, review_failed: 0 },
    });

    renderQueue();

    expect(await screen.findByText("2 blockers from code review")).toBeInTheDocument();
    expect(
      screen.queryByText("Checks are in — waiting on your approval"),
    ).not.toBeInTheDocument();
  });

  it("falls back to the agent's own question on an escalation row", async () => {
    apiMocks.getDecisionQueue.mockResolvedValue({
      items: [
        row({
          kind: "escalation",
          needed: "",
          escalation: {
            id: "esc-1",
            kind: "question",
            prompt: "CSV or XLSX?",
            detail: "",
            options: [],
          },
        }),
      ],
      total: 1,
      counts: { escalation: 1, merge_ready: 0, qa_failed: 0, review_failed: 0 },
    });

    renderQueue();

    expect(await screen.findByText("CSV or XLSX?")).toBeInTheDocument();
    expect(
      screen.queryByText("An agent is waiting on your answer"),
    ).not.toBeInTheDocument();
  });

  it("degrades a kind it has never heard of into a generic row that still opens the issue", async () => {
    apiMocks.getDecisionQueue.mockResolvedValue({
      items: [row({ kind: "deploy_approval", issue_id: "issue-7", identifier: "MUL-7" })],
      total: 1,
      counts: { escalation: 0, merge_ready: 0, qa_failed: 0, review_failed: 0 },
    });

    renderQueue();

    expect(await screen.findByLabelText("Decision")).toBeInTheDocument();
    expect(screen.getByText("Needs a decision from you")).toBeInTheDocument();
    expect(screen.getByRole("link")).toHaveAttribute("href", "/w/issues/issue-7");
    // Not batchable: an unknown kind has no known surface to step through.
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });

  it("renders no risk chip when the project has no opinion, and a chip when it does", async () => {
    apiMocks.getDecisionQueue.mockResolvedValue({
      items: [
        row({ risk_tier: "", title: "No opinion" }),
        row({ issue_id: "issue-2", identifier: "MUL-2", risk_tier: "critical", title: "Critical one" }),
        row({ issue_id: "issue-3", identifier: "MUL-3", risk_tier: "unclassified", title: "Untiered" }),
        row({ issue_id: "issue-4", identifier: "MUL-4", risk_tier: "nuclear", title: "Drifted tier" }),
      ],
      total: 4,
      counts: { escalation: 0, merge_ready: 4, qa_failed: 0, review_failed: 0 },
    });

    renderQueue();

    expect(await screen.findByText("critical")).toBeInTheDocument();
    // "unclassified" is the project saying "no opinion" out loud, and an
    // unknown tier is still information — both render, quietly.
    expect(screen.getByText("unclassified")).toBeInTheDocument();
    expect(screen.getByText("nuclear")).toBeInTheDocument();
    // The row with risk_tier "" gets no chip at all.
    expect(screen.queryByText("guarded")).not.toBeInTheDocument();
  });

  it("sends each kind to the surface that already handles it", async () => {
    apiMocks.getDecisionQueue.mockResolvedValue({
      items: [
        row({ kind: "merge_ready", issue_id: "issue-1", title: "Merge me" }),
        row({ kind: "qa_failed", issue_id: "issue-2", identifier: "MUL-2", title: "Judge me" }),
        row({ kind: "escalation", issue_id: "issue-3", identifier: "MUL-3", title: "Answer me" }),
        row({ kind: "review_failed", issue_id: "issue-4", identifier: "MUL-4", title: "Fix me" }),
      ],
      total: 4,
      counts: { escalation: 1, merge_ready: 1, qa_failed: 1, review_failed: 1 },
    });

    renderQueue();

    await screen.findByText("Merge me");
    const hrefs = screen.getAllByRole("link").map((a) => a.getAttribute("href"));
    expect(hrefs).toEqual([
      "/w/issues/issue-1?lens=work",
      "/w/issues/issue-2?lens=qa",
      // An escalation opens the issue, where the escalation card renders.
      "/w/issues/issue-3",
      "/w/issues/issue-4?lens=work",
    ]);
  });

  it("steps through the picked merge-ready rows one at a time in the review surface", async () => {
    const user = userEvent.setup();
    apiMocks.getDecisionQueue.mockResolvedValue({
      items: [
        row({ issue_id: "issue-1", identifier: "MUL-1", title: "First change" }),
        row({ issue_id: "issue-2", identifier: "MUL-2", title: "Second change" }),
        row({ issue_id: "issue-3", identifier: "MUL-3", title: "Third change" }),
        // A failed review is a judgement, not an approve tap — not batchable.
        row({ kind: "review_failed", issue_id: "issue-4", identifier: "MUL-4", title: "Fix me" }),
      ],
      total: 4,
      counts: { escalation: 0, merge_ready: 3, qa_failed: 0, review_failed: 1 },
    });

    renderQueue();

    await screen.findByText("First change");
    const boxes = screen.getAllByRole("checkbox");
    expect(boxes).toHaveLength(3);
    await user.click(boxes[0]!);
    await user.click(boxes[2]!);

    await user.click(screen.getByRole("button", { name: /Review 2 together/ }));

    expect(await screen.findByText("review surface issue-1")).toBeInTheDocument();
    expect(screen.getByText("1 of 2")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Next" }));
    expect(await screen.findByText("review surface issue-3")).toBeInTheDocument();
    expect(screen.getByText("2 of 2")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Previous" }));
    expect(await screen.findByText("review surface issue-1")).toBeInTheDocument();

    // The last item offers Done, which closes the pass.
    await user.click(screen.getByRole("button", { name: "Next" }));
    await user.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByText(/review surface/)).not.toBeInTheDocument());
  });

  it("shows a neutral error — NOT an all-clear — when the fetch fails", async () => {
    apiMocks.getDecisionQueue.mockRejectedValue(new Error("network down"));

    renderQueue();

    expect(await screen.findByText(/This is not an all-clear/)).toBeInTheDocument();
    expect(screen.queryByText(/Nothing is waiting on you/)).not.toBeInTheDocument();
  });

  it("is calm, not celebratory, when nothing is waiting", async () => {
    apiMocks.getDecisionQueue.mockResolvedValue({
      items: [],
      total: 0,
      counts: { escalation: 0, merge_ready: 0, qa_failed: 0, review_failed: 0 },
    });

    renderQueue();

    expect(await screen.findByText("Nothing is waiting on you right now.")).toBeInTheDocument();
  });
});
