import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { QALiveCases } from "./qa-live-cases";

// QALiveCases reads the issue's test cases (testCasesOptions → api.getIssueTestCases)
// and renders each with its live verdict. These tests cover its OWN logic: the
// self-hide on no cases, the pass/fail rollup, failure-first ordering, and that a
// failing case surfaces its one-line output.

const apiMocks = vi.hoisted(() => ({ getIssueTestCases: vi.fn() }));
vi.mock("@agora/core/api", () => ({ api: apiMocks }));

function mkCase(over: Record<string, unknown> = {}) {
  return {
    id: `c-${Math.random().toString(36).slice(2)}`,
    issue_id: "i-1",
    title: "case",
    steps: "",
    expected: "",
    kind: "automated",
    source: "agent",
    author_type: "agent",
    category: "positive",
    preconditions: "",
    priority: "p2",
    modality: "ui",
    criterion_ref: "",
    created_at: "2026-07-14T00:00:00Z",
    latest_run: null,
    ...over,
  };
}

function renderPanel(status = "in_review") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
        <QALiveCases issueId="i-1" status={status} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  apiMocks.getIssueTestCases.mockReset();
});

describe("QALiveCases", () => {
  it("renders nothing when the issue has no test cases", async () => {
    apiMocks.getIssueTestCases.mockResolvedValue({ test_cases: [] });
    const { container } = renderPanel();
    // let the query settle
    await Promise.resolve();
    expect(container).toBeEmptyDOMElement();
  });

  it("lists cases with a pass/fail rollup and surfaces a failure's output", async () => {
    apiMocks.getIssueTestCases.mockResolvedValue({
      test_cases: [
        mkCase({ title: "Greet button label", latest_run: { id: "r1", status: "pass", run_source: "agent", created_at: "", output: "", trace_path: "" } }),
        mkCase({ title: "Rejects wrong casing", category: "negative", latest_run: { id: "r2", status: "fail", run_source: "agent", created_at: "", output: "expected Greet, got Get greeting", trace_path: "" } }),
        mkCase({ title: "Pending smoke", latest_run: null }),
      ],
    });
    renderPanel();

    // all three titles present
    expect(await screen.findByText("Greet button label")).toBeTruthy();
    expect(screen.getByText("Rejects wrong casing")).toBeTruthy();
    expect(screen.getByText("Pending smoke")).toBeTruthy();

    // rollup: 1/3 passed, 1 failed (the two live in one parent span → substring match)
    expect(screen.getByText(/1\/3 passed/)).toBeTruthy();
    expect(screen.getByText(/1 failed/)).toBeTruthy();

    // the failing case's one-line output is shown
    expect(screen.getByText("expected Greet, got Get greeting")).toBeTruthy();
  });

  it("orders failures before pending before passes", async () => {
    apiMocks.getIssueTestCases.mockResolvedValue({
      test_cases: [
        mkCase({ title: "AAA pass", latest_run: { id: "r1", status: "pass", run_source: "agent", created_at: "", output: "", trace_path: "" } }),
        mkCase({ title: "BBB pending", latest_run: null }),
        mkCase({ title: "CCC fail", latest_run: { id: "r3", status: "fail", run_source: "agent", created_at: "", output: "boom", trace_path: "" } }),
      ],
    });
    renderPanel();
    await screen.findByText("CCC fail");
    const items = screen.getAllByRole("listitem").map((li) => li.textContent ?? "");
    const idxFail = items.findIndex((s) => s.includes("CCC fail"));
    const idxPending = items.findIndex((s) => s.includes("BBB pending"));
    const idxPass = items.findIndex((s) => s.includes("AAA pass"));
    expect(idxFail).toBeLessThan(idxPending);
    expect(idxPending).toBeLessThan(idxPass);
  });
});
