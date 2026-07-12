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
  // useQaRunningTasks (qa-live-progress.tsx, real hook — not mocked below) also
  // resolves the QA-squad agent id set via these two calls. No squad in these
  // tests → empty result → the hook's own "no QA squad → no filter" fallback,
  // matching pre-fix behavior for the "which issue is this task on" assertions
  // these tests actually care about.
  listSquads: vi.fn(),
  listSquadMembers: vi.fn(),
  listTestCaseRuns: vi.fn(),
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
// useQaRunningTasks stays REAL (imported via importOriginal) — the stop-
// affordance tests below assert its actual issue_id filtering against the
// mocked agent task snapshot, not a stubbed return value.
vi.mock("./qa-live-progress", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./qa-live-progress")>();
  return {
    ...actual,
    useRunningTestCaseId: qaLiveProgressMocks.useRunningTestCaseId,
    useLiveCaseVerdicts: qaLiveProgressMocks.useLiveCaseVerdicts,
  };
});

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

// FileBugSheet has its own coverage; here we only assert the panel's WIRING —
// that the per-case Bug action mounts it with the right derived caseSeed
// (title + failed step/note + criterion) — so it's stubbed to a marker that
// exposes the props it received.
const fileBugSheetMock = vi.hoisted(() => ({ lastProps: null as Record<string, unknown> | null }));
vi.mock("./file-bug-sheet", () => ({
  FileBugSheet: (props: Record<string, unknown>) => {
    fileBugSheetMock.lastProps = props;
    return <div data-testid="file-bug-sheet" />;
  },
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
    apiMocks.listSquads.mockResolvedValue([]);
    apiMocks.listSquadMembers.mockResolvedValue([]);
    apiMocks.listTestCaseRuns.mockResolvedValue({ runs: [] });
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
    apiMocks.listSquads.mockResolvedValue([]);
    apiMocks.listSquadMembers.mockResolvedValue([]);
    apiMocks.listTestCaseRuns.mockResolvedValue({ runs: [] });
    apiMocks.getAgentTaskSnapshot.mockResolvedValue([]);
    apiMocks.getIssue.mockResolvedValue({ id: "issue-1", description: null });
    apiMocks.cancelTaskById.mockResolvedValue({ id: "task-99", status: "cancelled" });
    qaLiveProgressMocks.useRunningTestCaseId.mockReturnValue(null);
    qaLiveProgressMocks.useLiveCaseVerdicts.mockReturnValue({});
  });

  it("marks the running row with a per-row 'Running' indicator (no duplicate sticky summary)", async () => {
    apiMocks.getIssueTestCases.mockResolvedValue({
      test_cases: [automatedCase({ id: "tc-1", title: "Checkout — happy path" }), automatedCase({ id: "tc-2", title: "Checkout — declined card" })],
    });
    qaLiveProgressMocks.useRunningTestCaseId.mockReturnValue("tc-2");

    renderPanel();

    await screen.findByText("Checkout — declined card");
    // The running fact now shows in exactly ONE place inside the panel: the
    // per-row highlight (relabeled "Running", not the uppercase "RUNS" badge).
    // The duplicate sticky "Running case X of N" summary is gone (the top strip
    // that carries the ✓/✗/N tally lives in QALiveProgress, not this panel).
    expect(screen.getByText("Running")).toBeInTheDocument();
    expect(screen.queryByText(/Running case/)).not.toBeInTheDocument();
    expect(screen.queryByText("RUNS")).not.toBeInTheDocument();
  });

  it("marks no row when the live marker names a case not in the list", async () => {
    apiMocks.getIssueTestCases.mockResolvedValue({ test_cases: [automatedCase()] });
    qaLiveProgressMocks.useRunningTestCaseId.mockReturnValue("unknown-case-id");

    renderPanel();

    await screen.findByText("Checkout — happy path");
    // No row matches the marker → no per-row "Running" highlight in the panel
    // (the generic "Running QA…" message lives in the top strip, not here).
    expect(screen.queryByText("Running")).not.toBeInTheDocument();
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
    apiMocks.listSquads.mockResolvedValue([]);
    apiMocks.listSquadMembers.mockResolvedValue([]);
    apiMocks.listTestCaseRuns.mockResolvedValue({ runs: [] });
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

describe("TestCasesPanel — per-step manual run (checklist)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.listSquads.mockResolvedValue([]);
    apiMocks.listSquadMembers.mockResolvedValue([]);
    apiMocks.listTestCaseRuns.mockResolvedValue({ runs: [] });
    apiMocks.getAgentTaskSnapshot.mockResolvedValue([]);
    apiMocks.getIssue.mockResolvedValue({ id: "issue-1", description: null });
    // Manual cases: the primary Run opens the step checklist directly.
    apiMocks.getIssueTestCases.mockResolvedValue({ test_cases: [automatedCase({ kind: "manual" })] });
    apiMocks.recordTestCaseRun.mockResolvedValue({});
    qaLiveProgressMocks.useRunningTestCaseId.mockReturnValue(null);
    qaLiveProgressMocks.useLiveCaseVerdicts.mockReturnValue({});
  });

  it("walks the steps, notes the failure, and records ONE run with the fenced breakdown", async () => {
    renderPanel();

    fireEvent.click(await screen.findByTitle("Run the steps manually"));
    expect(screen.getByText("Step 1 of 2")).toBeInTheDocument();

    // Step 1 passes; header advances to step 2.
    fireEvent.click(screen.getAllByTitle("Pass")[0]!);
    expect(screen.getByText("Step 2 of 2")).toBeInTheDocument();

    // Step 2 fails — the actual-result note input appears.
    fireEvent.click(screen.getAllByTitle("Fail")[1]!);
    fireEvent.change(screen.getByLabelText("Actual result — what happened?"), {
      target: { value: "cart total went negative" },
    });

    fireEvent.click(screen.getByRole("button", { name: "Record result" }));

    await waitFor(() => expect(apiMocks.recordTestCaseRun).toHaveBeenCalledTimes(1));
    const [caseId, body] = apiMocks.recordTestCaseRun.mock.calls[0]!;
    expect(caseId).toBe("tc-1");
    expect(body.status).toBe("fail");
    expect(body.output).toContain("Manual step run — 1/2 passed, failed at step 2");
    expect(body.output).toContain("step-results");
    expect(body.output).toContain("cart total went negative");
  });

  it("allows finishing early on a fail — unmarked steps record as skipped", async () => {
    renderPanel();

    fireEvent.click(await screen.findByTitle("Run the steps manually"));
    // Fail step 1 immediately; step 2 stays unmarked.
    fireEvent.click(screen.getAllByTitle("Fail")[0]!);
    fireEvent.click(screen.getByRole("button", { name: "Record result" }));

    await waitFor(() => expect(apiMocks.recordTestCaseRun).toHaveBeenCalledTimes(1));
    const [, body] = apiMocks.recordTestCaseRun.mock.calls[0]!;
    expect(body.status).toBe("fail");
    expect(body.output).toContain('{"step":2,"status":"skip"}');
  });

  it("renders a recorded walk as a structured per-step breakdown, not raw fence text", async () => {
    const output =
      'Manual step run — 1/2 passed, failed at step 2\n```step-results\n[{"step":1,"status":"pass"},{"step":2,"status":"fail","note":"toast never appeared"}]\n```';
    apiMocks.getIssueTestCases.mockResolvedValue({
      test_cases: [
        automatedCase({
          kind: "manual",
          latest_run: {
            id: "run-1",
            status: "fail",
            run_source: "human",
            created_at: "2026-01-01T00:00:00Z",
            output,
            trace_path: "",
          },
        }),
      ],
    });

    renderPanel();

    // hasReason auto-opens the failing row; the breakdown renders structured.
    expect(await screen.findByText("Manual step run")).toBeInTheDocument();
    expect(screen.getByText(/toast never appeared/)).toBeInTheDocument();
    // The raw fenced JSON must not leak into the DOM as text.
    expect(screen.queryByText(/step-results/)).not.toBeInTheDocument();
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

describe("TestCasesPanel — re-run failed + per-case file-bug (Phase 2)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    fileBugSheetMock.lastProps = null;
    apiMocks.listSquads.mockResolvedValue([]);
    apiMocks.listSquadMembers.mockResolvedValue([]);
    apiMocks.getAgentTaskSnapshot.mockResolvedValue([]);
    apiMocks.getIssue.mockResolvedValue({
      id: "issue-1",
      title: "Fix onboarding",
      identifier: "MUL-1",
      project_id: "project-1",
      description: null,
    });
    apiMocks.sliceAction.mockResolvedValue({});
    qaLiveProgressMocks.useRunningTestCaseId.mockReturnValue(null);
    qaLiveProgressMocks.useLiveCaseVerdicts.mockReturnValue({});
  });

  const failRun = (output: string) => ({
    id: "run-1",
    status: "fail" as const,
    run_source: "human",
    created_at: "2026-01-01T00:00:00Z",
    output,
    trace_path: "",
  });

  it("Re-run failed (N) fires ONE set-scoped run_test_cases naming only the failing case ids", async () => {
    apiMocks.getIssueTestCases.mockResolvedValue({
      test_cases: [
        automatedCase({ id: "tc-fail-1", title: "A", latest_run: failRun("boom") }),
        automatedCase({ id: "tc-fail-2", title: "B", latest_run: failRun("boom2") }),
        automatedCase({
          id: "tc-pass",
          title: "C",
          latest_run: { id: "run-3", status: "pass", run_source: "agent", created_at: "", output: "", trace_path: "" },
        }),
      ],
    });

    renderPanel();

    const btn = await screen.findByRole("button", { name: /Re-run failed \(2\)/ });
    fireEvent.click(btn);

    await waitFor(() => expect(apiMocks.sliceAction).toHaveBeenCalledTimes(1));
    const [issueId, body] = apiMocks.sliceAction.mock.calls[0]!;
    expect(issueId).toBe("issue-1");
    expect(body.kind).toBe("run_test_cases");
    // The SET scope marker the server enforces fail-closed
    // (scopedTestCaseIDsFromTrigger): both failing ids, NOT the passing one.
    expect(body.scope).toContain("RUN ONLY these test cases ids=tc-fail-1,tc-fail-2");
    expect(body.scope).not.toContain("tc-pass");
  });

  it("hides the Re-run failed button when nothing is failing", async () => {
    apiMocks.getIssueTestCases.mockResolvedValue({
      test_cases: [
        automatedCase({
          id: "tc-pass",
          latest_run: { id: "run-1", status: "pass", run_source: "agent", created_at: "", output: "", trace_path: "" },
        }),
      ],
    });

    renderPanel();

    await screen.findByText("Checkout — happy path");
    expect(screen.queryByRole("button", { name: /Re-run failed/ })).not.toBeInTheDocument();
  });

  it("per-case Bug action mounts FileBugSheet seeded with title + failed step/note + criterion", async () => {
    apiMocks.getIssueTestCases.mockResolvedValue({
      test_cases: [
        automatedCase({
          id: "tc-fail-1",
          title: "Checkout — declined card",
          criterion_ref: "AC2",
          latest_run: failRun(
            "Manual step run — 1/2 passed, failed at step 2\n```step-results\n[{\"step\":1,\"status\":\"pass\"},{\"step\":2,\"status\":\"fail\",\"note\":\"no error toast\"}]\n```",
          ),
        }),
      ],
    });

    renderPanel();

    // File-bug now lives in the row's ⋯ overflow (not an always-there button).
    fireEvent.click(await screen.findByTitle("More actions"));
    fireEvent.click(screen.getByRole("menuitem", { name: "File a bug from this case" }));

    await screen.findByTestId("file-bug-sheet");
    const props = fileBugSheetMock.lastProps!;
    expect(props.sourceId).toBe("issue-1");
    expect(props.identifier).toBe("MUL-1");
    const seed = props.caseSeed as { title: string; detail: string; criterionRef?: string };
    expect(seed.title).toBe("Checkout — declined card");
    expect(seed.detail).toBe("failed at step 2 — no error toast");
    expect(seed.criterionRef).toBe("AC2");
  });

  it("never offers the Bug action on a passing row", async () => {
    apiMocks.getIssueTestCases.mockResolvedValue({
      test_cases: [
        automatedCase({
          id: "tc-pass",
          latest_run: { id: "run-1", status: "pass", run_source: "agent", created_at: "", output: "", trace_path: "" },
        }),
      ],
    });

    renderPanel();

    await screen.findByText("Checkout — happy path");
    // The passing automated case still has a ⋯ (hand-walk its steps), but no
    // File-a-bug item — that lives on failing/blocked rows only.
    fireEvent.click(screen.getByTitle("More actions"));
    expect(screen.queryByRole("menuitem", { name: "File a bug from this case" })).not.toBeInTheDocument();
  });
});

describe("TestCasesPanel — flaky chip + run history (Phase 3)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.listSquads.mockResolvedValue([]);
    apiMocks.listSquadMembers.mockResolvedValue([]);
    apiMocks.listTestCaseRuns.mockResolvedValue({ runs: [] });
    apiMocks.getAgentTaskSnapshot.mockResolvedValue([]);
    apiMocks.getIssue.mockResolvedValue({ id: "issue-1", description: null });
    qaLiveProgressMocks.useRunningTestCaseId.mockReturnValue(null);
    qaLiveProgressMocks.useLiveCaseVerdicts.mockReturnValue({});
  });

  it("shows the amber flaky chip in the expanded row detail, not the collapsed row", async () => {
    apiMocks.getIssueTestCases.mockResolvedValue({
      test_cases: [
        automatedCase({ id: "tc-flaky", title: "Wobbly case", flaky: true }),
        automatedCase({ id: "tc-solid", title: "Solid case" }),
      ],
    });

    renderPanel();

    await screen.findByText("Wobbly case");
    // flaky is one of the 8 meta tokens moved behind the row expand — it must
    // NOT clutter the collapsed row.
    expect(screen.queryByText("flaky")).not.toBeInTheDocument();
    // Expanding the flaky row reveals it (exactly once).
    fireEvent.click(screen.getByText("Wobbly case"));
    expect(await screen.findByText("flaky")).toBeInTheDocument();
    expect(screen.getAllByText("flaky")).toHaveLength(1);
  });

  it("fetches and renders the last-5 run history dots when a row expands", async () => {
    apiMocks.getIssueTestCases.mockResolvedValue({
      test_cases: [
        automatedCase({
          id: "tc-hist",
          title: "History case",
          // A failing latest run auto-opens the row (hasReason), which enables
          // the history query without an extra click.
          latest_run: {
            id: "run-9",
            status: "fail",
            run_source: "agent",
            created_at: "2026-07-10T12:00:00Z",
            output: "boom",
            trace_path: "",
          },
        }),
      ],
    });
    apiMocks.listTestCaseRuns.mockResolvedValue({
      runs: [
        { id: "r1", status: "fail", run_source: "agent", created_at: "2026-07-10T12:00:00Z", commit_sha: "deadbeef1234", session_id: "", started_at: "", finished_at: "", output: "", trace_path: "" },
        { id: "r2", status: "pass", run_source: "human", created_at: "2026-07-09T12:00:00Z", commit_sha: "", session_id: "", started_at: "", finished_at: "", output: "", trace_path: "" },
      ],
    });

    renderPanel();

    await screen.findByText("History case");
    await screen.findByText("Recent runs:"); // test_cases.run_history label renders once loaded
    await waitFor(() => expect(apiMocks.listTestCaseRuns).toHaveBeenCalledWith("tc-hist"));
    // The newest dot's tooltip carries verdict + source + short sha.
    expect(screen.getByTitle(/fail · agent · deadbee/)).toBeInTheDocument();
  });
});
