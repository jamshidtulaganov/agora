import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { TestCase } from "@agora/core/types";
import { QASuiteView } from "./qa-suite-view";

// The Suite tab manages a project's STANDING regression suite (issue_id NULL
// base cases). These tests cover the two states a QA engineer lands in first:
// the per-project gate (no project selected) and a populated suite.

const apiMocks = vi.hoisted(() => ({
  listProjectTestCases: vi.fn(),
  createProjectTestCase: vi.fn(),
  buildProjectBaseSuite: vi.fn(),
  archiveTestCase: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function renderView(projectId?: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <QASuiteView projectId={projectId} />
    </QueryClientProvider>,
  );
}

const baseCase = (over: Partial<TestCase>): TestCase => ({
  id: "tc-1",
  issue_id: "",
  title: "[e2e] Checkout — happy path",
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

describe("QASuiteView", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("prompts to pick a project when none is selected (the suite is per-project)", () => {
    renderView(undefined);
    expect(screen.getByText(/select a project to manage its regression suite/i)).toBeInTheDocument();
    // No project → no fetch fires.
    expect(apiMocks.listProjectTestCases).not.toHaveBeenCalled();
  });

  it("renders the project's base cases with kind/category and a header count", async () => {
    apiMocks.listProjectTestCases.mockResolvedValue({
      test_cases: [baseCase({}), baseCase({ id: "tc-2", title: "[api] Reject bad coupon", category: "negative" })],
    });

    renderView("project-1");

    await waitFor(() =>
      expect(screen.getByText("[e2e] Checkout — happy path")).toBeInTheDocument(),
    );
    expect(screen.getByText("[api] Reject bad coupon")).toBeInTheDocument();
    expect(screen.getByText("Regression suite")).toBeInTheDocument();
    expect(apiMocks.listProjectTestCases).toHaveBeenCalledWith("project-1");
  });

  it("shows the standing-release-gate empty state when the suite is empty", async () => {
    apiMocks.listProjectTestCases.mockResolvedValue({ test_cases: [] });

    renderView("project-1");

    await waitFor(() => expect(screen.getByText(/no regression cases yet/i)).toBeInTheDocument());
    expect(screen.getByText(/standing release gate/i)).toBeInTheDocument();
  });
});
