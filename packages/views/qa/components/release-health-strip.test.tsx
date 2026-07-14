import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { ReleaseHealthStrip } from "./release-health-strip";

// The Release page's health strip — a single readiness roll-up line plus the
// needs-decision chip. These tests pin the strip's contract: silent when
// there are no sprints, a roll-up ("M of N sprints ready to ship") that
// deep-links to Ship (no per-sprint rows, no ✓✗◌ trio), and a chip that counts
// fail / pass_with_failing_cases and deep-links to Queue with the needs-human
// toggle pre-set.

const apiMocks = vi.hoisted(() => ({
  getSprintReadiness: vi.fn(),
  listIssues: vi.fn(),
  listQAVerdicts: vi.fn(),
  getAgentTaskSnapshot: vi.fn(),
  listSquads: vi.fn(),
  listSquadMembers: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));

function renderStrip(over?: { onOpenShip?: () => void; onOpenQueueNeedsHuman?: () => void }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={qc}>
        <ReleaseHealthStrip
          onOpenShip={over?.onOpenShip ?? vi.fn()}
          onOpenQueueNeedsHuman={over?.onOpenQueueNeedsHuman ?? vi.fn()}
        />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

const sprint = (over: Record<string, unknown>) => ({
  sprint_id: "sp-1",
  name: "Sprint 12",
  branch: "sprint/12",
  project_id: "p-1",
  project_title: "SD Main",
  total: 5,
  passed: 2,
  failed: 1,
  pending: 2,
  no_qa: 1,
  mergeable: false,
  regression: { status: "failed", source: "", triggered_at: "", completed_at: "", reason: "", run_issue_id: "" },
  issues: [],
  ...over,
});

const issue = (id: string, labels: string[]) =>
  ({ id, labels: labels.map((name) => ({ id: name, name, color: "" })) }) as never;

describe("ReleaseHealthStrip", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.listIssues.mockResolvedValue({ issues: [] });
    apiMocks.listQAVerdicts.mockResolvedValue({ verdicts: {} });
    apiMocks.getAgentTaskSnapshot.mockResolvedValue([]);
    apiMocks.listSquads.mockResolvedValue([]);
    apiMocks.listSquadMembers.mockResolvedValue([]);
  });

  it("renders nothing when there are no active sprints", async () => {
    apiMocks.getSprintReadiness.mockResolvedValue({ sprints: [] });

    const { container } = renderStrip();

    await waitFor(() => expect(apiMocks.getSprintReadiness).toHaveBeenCalled());
    await waitFor(() => expect(container).toBeEmptyDOMElement());
  });

  it("rolls readiness up into one line — no per-sprint rows or ✓✗◌ trio", async () => {
    apiMocks.getSprintReadiness.mockResolvedValue({ sprints: [sprint({})] });

    renderStrip();

    // A single roll-up ("M of N sprints ready to ship"); the non-mergeable
    // fixture is 0 of 1. The per-sprint name row, ring, and count trio are gone.
    expect(await screen.findByText("0 of 1 sprints ready to ship")).toBeInTheDocument();
    expect(screen.queryByText("SD Main · Sprint 12")).not.toBeInTheDocument();
    expect(screen.queryByTitle("passed")).not.toBeInTheDocument();
    expect(screen.queryByTitle("failing")).not.toBeInTheDocument();
  });

  it("clicking the roll-up opens Ship", async () => {
    apiMocks.getSprintReadiness.mockResolvedValue({ sprints: [sprint({ mergeable: true })] });
    const onOpenShip = vi.fn();

    renderStrip({ onOpenShip });

    const rollup = await screen.findByText("1 of 1 sprints ready to ship");
    fireEvent.click(rollup);
    expect(onOpenShip).toHaveBeenCalledTimes(1);
  });

  it("shows the needs-decision chip (server reconciled_state + label fallback) and deep-links to Queue", async () => {
    apiMocks.getSprintReadiness.mockResolvedValue({ sprints: [sprint({})] });
    apiMocks.listIssues.mockResolvedValue({
      issues: [
        issue("i-1", ["qa:fail"]), // label fallback → fail
        issue("i-2", ["qa:pass"]), // server state overrides the passing label
        issue("i-3", ["qa:pass"]), // genuinely passed — must NOT count
      ],
    });
    apiMocks.listQAVerdicts.mockResolvedValue({
      verdicts: { "i-2": { reconciled_state: "pass_with_failing_cases" } },
    });
    const onOpenQueueNeedsHuman = vi.fn();

    renderStrip({ onOpenQueueNeedsHuman });

    const chip = await screen.findByText("2 need a decision");
    fireEvent.click(chip);
    expect(onOpenQueueNeedsHuman).toHaveBeenCalledTimes(1);
  });

  it("excludes issues whose QA gate is running right now, matching the Queue's needs-human cut", async () => {
    apiMocks.getSprintReadiness.mockResolvedValue({ sprints: [sprint({})] });
    apiMocks.listIssues.mockResolvedValue({
      issues: [
        issue("i-1", ["qa:fail"]), // gate re-running → the Queue shows "running", not needs-human
        issue("i-2", ["qa:fail"]), // no live run → genuinely needs a decision
      ],
    });
    apiMocks.listQAVerdicts.mockResolvedValue({ verdicts: {} });
    apiMocks.getAgentTaskSnapshot.mockResolvedValue([
      { id: "t-1", issue_id: "i-1", agent_id: "a-1", status: "running" },
    ]);

    renderStrip();

    expect(await screen.findByText("1 need a decision")).toBeInTheDocument();
  });
});
