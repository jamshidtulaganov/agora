import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { IssueExecution } from "./issue-execution";

const mocks = vi.hoisted(() => ({
  getIssueOrchestration: vi.fn(),
  getIssue: vi.fn(),
  getProject: vi.fn(),
  listTasksByIssue: vi.fn(),
  listAgents: vi.fn(),
  listSquads: vi.fn(),
  createIssueOrchestration: vi.fn(),
  editIssueOrchestration: vi.fn(),
  respondToOrchestrationStep: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({
  api: {
    getIssueOrchestration: mocks.getIssueOrchestration,
    getIssue: mocks.getIssue,
    getProject: mocks.getProject,
    listTasksByIssue: mocks.listTasksByIssue,
    listAgents: mocks.listAgents,
    listSquads: mocks.listSquads,
    createIssueOrchestration: mocks.createIssueOrchestration,
    editIssueOrchestration: mocks.editIssueOrchestration,
    respondToOrchestrationStep: mocks.respondToOrchestrationStep,
  },
}));
vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "workspace-1" }));
vi.mock("@agora/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: () => "Agent" }),
}));
vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("./stage-live-process", () => ({
  useTaskLive: () => ({ todos: [], headline: "", latestMessageAt: undefined }),
}));

function renderExecution() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={client}>
        <IssueExecution issueId="issue-1" />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("IssueExecution idle surface", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.getIssueOrchestration.mockResolvedValue(null);
    mocks.getIssue.mockResolvedValue({
      id: "issue-1",
      workspace_id: "workspace-1",
      title: "Issue",
      project_id: "project-1",
    });
    mocks.getProject.mockResolvedValue({
      id: "project-1",
      settings: {
        orchestration: {
          execution_strategy: "solo",
          progression_policy: "gated",
          max_concurrency: 5,
          review_plan_first: false,
        },
      },
    });
    mocks.listTasksByIssue.mockResolvedValue([]);
    mocks.listAgents.mockResolvedValue([]);
    mocks.listSquads.mockResolvedValue([]);
    mocks.createIssueOrchestration.mockResolvedValue({
      id: "run-1",
      issue_id: "issue-1",
      status: "running",
      mode: "auto",
      execution_strategy: "solo",
      progression_policy: "gated",
      policy: {},
      owner_type: "agent",
      base_git_states: [],
      execution_mode: "direct",
      plan_version: 1,
      revisions: [],
      created_at: "2026-08-06T00:00:00Z",
      updated_at: "2026-08-06T00:00:00Z",
      events: [],
      messages: [],
      steps: [],
    });
  });

  it("does not prompt with the Execution plan empty card on every issue", async () => {
    renderExecution();

    expect(await screen.findByRole("button", { name: "Start execution plan" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Execution plan" })).not.toBeInTheDocument();
    expect(
      screen.queryByText("Route this issue across planning, development, QA, review, and release approval."),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText("Uses this project's execution defaults. Customize only for this run."),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Build and run" })).not.toBeInTheDocument();
  });

  it("starts with project defaults via an empty create payload", async () => {
    renderExecution();

    fireEvent.click(await screen.findByRole("button", { name: "Start execution plan" }));

    await waitFor(() => {
      expect(mocks.createIssueOrchestration).toHaveBeenCalledWith("issue-1", {});
    });
  });

  it("keeps Customize as an opt-in override path", async () => {
    renderExecution();

    fireEvent.click(await screen.findByRole("button", { name: "Customize" }));

    expect(
      await screen.findByText("The assignee selects the topology; this project supplies progression, Squad concurrency, and review defaults."),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Build and run" })).toBeInTheDocument();
  });

  it("inherits project behavior without inheriting project topology", async () => {
    renderExecution();
    fireEvent.click(await screen.findByRole("button", { name: "Customize" }));
    fireEvent.click(await screen.findByRole("button", { name: "Build and run" }));

    await waitFor(() => {
      expect(mocks.createIssueOrchestration).toHaveBeenCalledWith("issue-1", {
        auto_start: true,
        progression_policy: "gated",
      });
    });
  });

  it("does not allow Customize to overwrite defaults while the project is loading", async () => {
    let resolveProject!: (value: unknown) => void;
    mocks.getProject.mockReturnValue(new Promise((resolve) => { resolveProject = resolve; }));
    renderExecution();

    const customize = await screen.findByRole("button", { name: "Customize" });
    expect(customize).toBeDisabled();
    resolveProject({
      id: "project-1",
      settings: { orchestration: {
        execution_strategy: "squad",
        progression_policy: "manual",
        max_concurrency: 5,
        review_plan_first: true,
      } },
    });
    await waitFor(() => expect(customize).toBeEnabled());
  });
});

describe("IssueExecution orchestration response", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    const run = {
      id: "run-1",
      issue_id: "issue-1",
      status: "waiting_input",
      mode: "auto",
      execution_strategy: "solo",
      progression_policy: "automatic",
      policy: {},
      owner_type: "agent",
      base_git_states: [],
      execution_mode: "direct",
      plan_version: 1,
      revisions: [],
      created_at: "2026-08-06T00:00:00Z",
      updated_at: "2026-08-06T00:00:00Z",
      events: [],
      messages: [],
      steps: [{
        id: "step-1",
        key: "plan",
        title: "Clarify API scope",
        stage: "plan",
        status: "waiting_input",
        position: 0,
        agent_id: "agent-1",
        task_id: "task-1",
        question_id: "question-2",
        approval_required: false,
        attempt: 1,
        max_attempts: 2,
        instructions: "",
        output: {
          schema_version: 1,
          stage: "plan",
          outcome: "waiting_input",
          summary: "API scope is ambiguous",
          decisions: [],
          contracts: [],
          artifacts: [],
          verification: [],
          findings: [],
          risks: [],
          blockers: [],
          next_actions: [],
          question: { prompt: "Should this use v1 or v2?", target: "human", blocking: true },
        },
        depends_on_step_ids: [],
        merge_status: "not_checked",
        conflict_files: [],
        kind: "task",
        capability: "coordination",
        integration_status: "not_required",
        integrated_head_shas: [],
        missing_head_shas: [],
      }],
    };
    mocks.getIssueOrchestration.mockResolvedValue(run);
    mocks.respondToOrchestrationStep.mockResolvedValue(run);
    mocks.getIssue.mockResolvedValue({ id: "issue-1", workspace_id: "workspace-1", title: "Issue" });
    mocks.listTasksByIssue.mockResolvedValue([]);
    mocks.listAgents.mockResolvedValue([]);
    mocks.listSquads.mockResolvedValue([]);
  });

  it("submits the exact question identity rendered with the waiting step", async () => {
    renderExecution();

    const input = await screen.findByLabelText("Write the decision or missing context…");
    fireEvent.change(input, { target: { value: "Use v2." } });
    fireEvent.click(screen.getByRole("button", { name: "Respond" }));

    await waitFor(() => {
      expect(mocks.respondToOrchestrationStep).toHaveBeenCalledWith(
        "issue-1",
        "step-1",
        { question_id: "question-2", message: "Use v2." },
      );
    });
  });

  it("keeps draft reroutes inside the roster and sends the target model snapshot", async () => {
    const step = {
      id: "step-dev", key: "dev", title: "Implement UI", stage: "dev", status: "pending", position: 1,
      agent_id: "agent-1", model: "model-one", thinking_level: "medium", approval_required: false, attempt: 0, max_attempts: 2,
      instructions: "Implement the UI", output: null, depends_on_step_ids: [], merge_status: "not_checked",
      conflict_files: [], kind: "task", capability: "frontend", integration_status: "not_required",
      integrated_head_shas: [], missing_head_shas: [],
    };
    const run = {
      id: "run-draft", issue_id: "issue-1", status: "draft", mode: "auto", execution_strategy: "squad",
      progression_policy: "automatic", owner_type: "squad", execution_mode: "squad", plan_version: 1,
      policy: { squad_roster: [
        { agent_id: "agent-1", name: "One", role: "Frontend", capability: "frontend", model: "model-one", thinking_level: "medium", max_concurrent_tasks: 1 },
        { agent_id: "agent-2", name: "Two", role: "Frontend", capability: "frontend", model: "model-two", thinking_level: "high", max_concurrent_tasks: 1 },
      ] },
      base_git_states: [], revisions: [], created_at: "2026-08-06T00:00:00Z", updated_at: "2026-08-06T00:00:00Z",
      events: [], messages: [], steps: [step],
    };
    mocks.getIssueOrchestration.mockResolvedValue(run);
    mocks.editIssueOrchestration.mockResolvedValue(run);
    mocks.listAgents.mockResolvedValue([
      { id: "agent-1", name: "One", model: "model-one", archived_at: null },
      { id: "agent-2", name: "Two", model: "model-two", archived_at: null },
      { id: "agent-outside", name: "Outside", model: "model-outside", archived_at: null },
    ]);

    renderExecution();
    fireEvent.click(await screen.findByRole("button", { name: "Details" }));
    expect(screen.getAllByText(/Pinned thinking/).length).toBeGreaterThanOrEqual(3);
    const select = await screen.findByRole("combobox", { name: "Route Implement UI to a worker" });
    expect(screen.queryByRole("option", { name: "Outside" })).not.toBeInTheDocument();
    fireEvent.change(select, { target: { value: "agent-2" } });

    await waitFor(() => expect(mocks.editIssueOrchestration).toHaveBeenCalledWith("issue-1", {
      expected_version: 1,
      reason: "Reroute Implement UI in the draft proposal",
      operation: "reroute",
      step_id: "step-dev",
      agent_id: "agent-2",
      model: "model-two",
      instructions: "Implement the UI",
    }));
  });
});
