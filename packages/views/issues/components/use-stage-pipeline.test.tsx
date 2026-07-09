import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useStagePipeline } from "./use-stage-pipeline";

// Focused on the deploy P0 fix (docs/deploy-stage-research.md P0): deploySynced
// was always undefined, so the deploy stage could never reach "passed". This
// covers the ASSEMBLY of hasDeployTarget/deploySynced from the box + PR
// queries this hook already fetches — deriveStagePipeline's own state-machine
// rules are covered exhaustively in packages/core/issues/stage.test.ts.

const apiMocks = vi.hoisted(() => ({
  getIssue: vi.fn(),
  listLabelsForIssue: vi.fn(),
  getQAEvidence: vi.fn(),
  listIssuePullRequests: vi.fn(),
  mergeReadiness: vi.fn(),
  getAgentTaskSnapshot: vi.fn(),
  listSquads: vi.fn(),
  listSquadMembers: vi.fn(),
  listRemoteBoxes: vi.fn(),
}));

let remoteBoxesEnabled = true;

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("@agora/core/config", () => ({
  useConfigStore: (selector: (s: { remoteBoxesEnabled: boolean }) => unknown) =>
    selector({ remoteBoxesEnabled }),
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
    metadata: { pr_number: 42 },
    labels: [] as { id: string; name: string }[],
    created_at: "",
    updated_at: "",
    ...over,
  };
}

function box(over: Record<string, unknown> = {}) {
  return {
    id: "box-1",
    workspace_id: "ws-1",
    owner_id: "u1",
    label: "jamshid's box",
    ssh_host: "jamshid.sdteam.uz",
    ssh_user: "deploy",
    ssh_port: 22,
    deploy_pubkey: "",
    daemon_id: "daemon-1",
    status: "online",
    last_error: "",
    repo_url: "",
    work_dir: "/srv/app",
    last_branch: "feature/foo",
    project_id: "project-1",
    created_at: "",
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

function deployState(stages: { stage: string; state: string }[]) {
  return stages.find((s) => s.stage === "deploy")?.state;
}

describe("useStagePipeline — deploy signal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    remoteBoxesEnabled = true;
    apiMocks.getIssue.mockResolvedValue(baseIssue());
    apiMocks.listLabelsForIssue.mockResolvedValue([]);
    apiMocks.getQAEvidence.mockResolvedValue(null);
    apiMocks.listIssuePullRequests.mockResolvedValue({ pull_requests: [pullRequest()] });
    apiMocks.mergeReadiness.mockResolvedValue({ ready: false, tier: "light", gates: [], reviews: [] });
    apiMocks.getAgentTaskSnapshot.mockResolvedValue([]);
    apiMocks.listSquads.mockResolvedValue([]);
    apiMocks.listSquadMembers.mockResolvedValue([]);
    apiMocks.listRemoteBoxes.mockResolvedValue([box()]);
  });

  it("passes the deploy stage when the bound box's last sync matches the issue's PR branch", async () => {
    const { result } = renderPipeline();
    await waitFor(() => expect(deployState(result.current.stages)).toBe("passed"));
  });

  it("does not pass when the box last synced a different branch than the issue's PR", async () => {
    apiMocks.listRemoteBoxes.mockResolvedValue([box({ last_branch: "main" })]);
    const { result } = renderPipeline();
    await waitFor(() => expect(deployState(result.current.stages)).not.toBe("skipped"));
    expect(deployState(result.current.stages)).not.toBe("passed");
  });

  it("does not pass when the bound box is offline even if the branch matches", async () => {
    apiMocks.listRemoteBoxes.mockResolvedValue([box({ status: "offline" })]);
    const { result } = renderPipeline();
    await waitFor(() => expect(deployState(result.current.stages)).not.toBe("skipped"));
    expect(deployState(result.current.stages)).not.toBe("passed");
  });

  it("does not pass when the issue has no matching PR to compare branches against", async () => {
    apiMocks.getIssue.mockResolvedValue(baseIssue({ metadata: {} }));
    const { result } = renderPipeline();
    await waitFor(() => expect(deployState(result.current.stages)).not.toBe("skipped"));
    expect(deployState(result.current.stages)).not.toBe("passed");
  });

  it("skips the deploy stage entirely when the remote-boxes feature is disabled", async () => {
    remoteBoxesEnabled = false;
    const { result } = renderPipeline();
    await waitFor(() => expect(deployState(result.current.stages)).toBe("skipped"));
  });

  it("skips the deploy stage when no box is bound to the issue's project", async () => {
    apiMocks.listRemoteBoxes.mockResolvedValue([box({ project_id: "other-project" })]);
    const { result } = renderPipeline();
    await waitFor(() => expect(deployState(result.current.stages)).toBe("skipped"));
  });
});
