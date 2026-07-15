import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { DevLensBody } from "./dev-lens";

const mocks = vi.hoisted(() => ({
  getIssueOrchestration: vi.fn(),
  getIssueArtifact: vi.fn(),
  getIssue: vi.fn(),
  listTasksByIssue: vi.fn(),
  replace: vi.fn(),
  searchParams: new URLSearchParams("lens=dev"),
}));

vi.mock("@agora/core/api", () => ({
  api: {
    getIssueOrchestration: mocks.getIssueOrchestration,
    getIssueArtifact: mocks.getIssueArtifact,
    getIssue: mocks.getIssue,
    listTasksByIssue: mocks.listTasksByIssue,
  },
}));
vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@agora/core/workspace/hooks", () => ({
  useActorName: () => ({ getAgentName: (id: string) => id === "agent-1" ? "Frontend agent" : "Backend agent" }),
}));
vi.mock("../../navigation", () => ({
  useNavigation: () => ({
    pathname: "/demo/issues/issue-1",
    searchParams: mocks.searchParams,
    replace: mocks.replace,
  }),
}));
vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => <span data-testid="agent-avatar" /> }));
vi.mock("./stage-live-process", () => ({ StageLiveProcessBody: ({ taskId }: { taskId: string }) => <div>Live {taskId}</div> }));
vi.mock("./artifact-code-viewer", () => ({ ArtifactCodeViewer: () => <div>Artifact code viewer</div> }));
vi.mock("./artifact-runtime-panels", () => ({
  ArtifactPreviewPanel: () => <div>Exact preview panel</div>,
  ArtifactChecksPanel: () => <div>Exact checks panel</div>,
}));

function step(overrides: Record<string, unknown>) {
  return {
    id: "step-1",
    key: "frontend",
    title: "Build frontend",
    stage: "dev",
    status: "running",
    position: 1,
    agent_id: "agent-1",
    task_id: "task-1",
    approval_required: false,
    attempt: 1,
    max_attempts: 1,
    instructions: "",
    depends_on_step_ids: [],
    merge_status: "not_checked",
    conflict_files: [],
    kind: "task",
    capability: "frontend",
    integration_status: "not_required",
    integrated_head_shas: [],
    missing_head_shas: [],
    ...overrides,
  };
}

function task(overrides: Record<string, unknown>) {
  return {
    id: "task-1",
    agent_id: "agent-1",
    runtime_id: "runtime-1",
    issue_id: "issue-1",
    status: "running",
    priority: 0,
    dispatched_at: null,
    started_at: "2026-07-15T00:00:00Z",
    completed_at: null,
    result: null,
    error: null,
    created_at: "2026-07-15T00:00:00Z",
    ...overrides,
  };
}

function renderLens(search = "lens=dev") {
  mocks.searchParams = new URLSearchParams(search);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={client}>
        <DevLensBody issueId="issue-1" />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("DevLensBody", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.getIssueOrchestration.mockResolvedValue({
      id: "run-1",
      issue_id: "issue-1",
      status: "running",
      steps: [
        step({}),
        step({ id: "step-2", key: "backend", title: "Build backend", agent_id: "agent-2", task_id: "task-2", position: 2 }),
      ],
    });
    mocks.listTasksByIssue.mockResolvedValue([
      task({}),
      task({ id: "task-2", agent_id: "agent-2" }),
    ]);
    mocks.getIssueArtifact.mockResolvedValue({
      run_id: "run-1",
      run_status: "running",
      ready: false,
      components: [],
      daemon_url: "",
      capabilities: {},
    });
    mocks.getIssue.mockResolvedValue({
      id: "issue-1",
      workspace_id: "ws-1",
      title: "Ship the requested feature",
      description: "Use the issue acceptance criteria as the worker brief.",
    });
  });

  it("shows parallel workers as separate activity lanes", async () => {
    renderLens();
    expect(await screen.findByText("Build frontend")).toBeInTheDocument();
    expect(screen.getByText("Build backend")).toBeInTheDocument();
    expect(screen.getByText("2 development workers")).toBeInTheDocument();
    expect(screen.getByText("2 working now")).toBeInTheDocument();
    expect(screen.getByText("Live task-1")).toBeInTheDocument();
    expect(screen.queryByText(/editor worktree/i)).not.toBeInTheDocument();
  });

  it("keeps the selected Dev workspace tab in the URL", async () => {
    renderLens();
    fireEvent.click(screen.getByRole("tab", { name: "Changes" }));
    expect(mocks.replace).toHaveBeenCalledWith("/demo/issues/issue-1?lens=dev&dev_tab=changes");
  });

  it("returns to the issue conversation without leaving stale lens params", async () => {
    renderLens("lens=dev&dev_tab=changes&focus=activity");
    fireEvent.click(screen.getByRole("button", { name: "Back to issue" }));
    expect(mocks.replace).toHaveBeenCalledWith("/demo/issues/issue-1?focus=activity");
  });

  it("renders the Agora-native artifact viewer for the Changes tab", async () => {
    renderLens("lens=dev&dev_tab=changes");
    expect(await screen.findByText("Artifact code viewer")).toBeInTheDocument();
  });

  it("shows committed handoff evidence instead of an editor session", async () => {
    mocks.getIssueOrchestration.mockResolvedValue({
      id: "run-1",
      issue_id: "issue-1",
      status: "running",
      steps: [step({
        status: "completed",
        head_sha: "a".repeat(40),
        merge_status: "clean",
        output: { output: "PROGRESS: Finishing\n\nImplemented the requested feature.\n\nExact commit is ready." },
      })],
    });
    mocks.listTasksByIssue.mockResolvedValue([task({ status: "completed", completed_at: "2026-07-15T00:10:00Z" })]);
    renderLens();
    expect(await screen.findByText("Committed handoff ready")).toBeInTheDocument();
    expect(screen.getByText("Use the issue acceptance criteria as the worker brief.")).toBeInTheDocument();
    expect(screen.getByText(/Implemented the requested feature/)).toBeInTheDocument();
    expect(screen.getByText("aaaaaaaa")).toBeInTheDocument();
    expect(screen.getByText("clean")).toBeInTheDocument();
  });
});
