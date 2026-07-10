import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useStagePipeline } from "./use-stage-pipeline";

// Covers the ASSEMBLY of StagePipelineInput from the queries this hook
// fetches (issue metadata -> prNumber, PR list -> prMerged, task snapshot +
// QA-squad membership -> running-stage attribution). deriveStagePipeline's
// own state-machine rules are covered exhaustively in
// packages/core/issues/stage.test.ts.
//
// Deploy is deliberately absent: it left the issue-level pipeline (deploy
// cycle rehome, part 1) — the hook no longer queries remote boxes or deploy
// events. Design is deliberately absent too: it left the stepper as its own
// stage (design is now a dev-build INPUT, not a pipeline stage — see
// packages/core/issues/stage.ts), so this hook no longer queries qa_evidence
// for a design verdict. The pipeline is 3 stages (dev/qa/review).

const apiMocks = vi.hoisted(() => ({
  getIssue: vi.fn(),
  listLabelsForIssue: vi.fn(),
  listIssuePullRequests: vi.fn(),
  mergeReadiness: vi.fn(),
  getAgentTaskSnapshot: vi.fn(),
  listSquads: vi.fn(),
  listSquadMembers: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));

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
    metadata: { pr_number: 42 },
    labels: [] as { id: string; name: string }[],
    created_at: "",
    updated_at: "",
    ...over,
  };
}

function pullRequest(over: Record<string, unknown> = {}) {
  return {
    id: "pr-1",
    workspace_id: "ws-1",
    repo_owner: "acme",
    repo_name: "app",
    number: 42,
    title: "Fix onboarding",
    state: "open",
    html_url: "",
    branch: "feature/foo",
    author_login: null,
    author_avatar_url: null,
    merged_at: null,
    closed_at: null,
    pr_created_at: "",
    pr_updated_at: "",
    ...over,
  };
}

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

function renderPipeline() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderHook(() => useStagePipeline("ws-1", "issue-1"), { wrapper: createWrapper(qc) });
}

function stageState(stages: { stage: string; state: string }[], stage: string) {
  return stages.find((s) => s.stage === stage)?.state;
}

describe("useStagePipeline — 3-stage assembly", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.getIssue.mockResolvedValue(baseIssue());
    apiMocks.listLabelsForIssue.mockResolvedValue([]);
    apiMocks.listIssuePullRequests.mockResolvedValue({ pull_requests: [pullRequest()] });
    apiMocks.mergeReadiness.mockResolvedValue({ ready: false, tier: "light", gates: [], reviews: [] });
    apiMocks.getAgentTaskSnapshot.mockResolvedValue([]);
    apiMocks.listSquads.mockResolvedValue([]);
    apiMocks.listSquadMembers.mockResolvedValue([]);
  });

  it("derives a 3-stage pipeline — design and deploy are not stages anymore", async () => {
    const { result } = renderPipeline();
    await waitFor(() => expect(stageState(result.current.stages, "dev")).toBe("passed"));
    expect(result.current.stages.map((s) => s.stage)).toEqual(["dev", "qa", "review"]);
  });

  it("passes the dev stage from the issue's pr_number metadata", async () => {
    const { result } = renderPipeline();
    await waitFor(() => expect(stageState(result.current.stages, "dev")).toBe("passed"));
  });

  it("keeps dev open when the issue has no PR metadata", async () => {
    apiMocks.getIssue.mockResolvedValue(baseIssue({ metadata: {} }));
    const { result } = renderPipeline();
    await waitFor(() => expect(stageState(result.current.stages, "dev")).toBe("active"));
  });

  it("passes the review stage when the matched PR is merged", async () => {
    apiMocks.listIssuePullRequests.mockResolvedValue({
      pull_requests: [pullRequest({ state: "merged" })],
    });
    const { result } = renderPipeline();
    await waitFor(() => expect(stageState(result.current.stages, "review")).toBe("passed"));
  });

  it("attributes a running QA-squad agent task to the qa stage, others to dev", async () => {
    apiMocks.getIssue.mockResolvedValue(baseIssue({ metadata: {} }));
    apiMocks.listSquads.mockResolvedValue([{ id: "sq-1", name: "QA Squad", leader_id: "agent-qa" }]);
    apiMocks.listSquadMembers.mockResolvedValue([]);
    apiMocks.getAgentTaskSnapshot.mockResolvedValue([
      { issue_id: "issue-1", agent_id: "agent-qa", status: "running" },
      { issue_id: "issue-1", agent_id: "agent-dev", status: "running" },
      { issue_id: "other-issue", agent_id: "agent-dev", status: "running" },
    ]);
    const { result } = renderPipeline();
    await waitFor(() => expect(stageState(result.current.stages, "qa")).toBe("running"));
    expect(stageState(result.current.stages, "dev")).toBe("running");
  });
});
