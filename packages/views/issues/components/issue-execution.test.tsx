import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { IssueExecution } from "./issue-execution";

const mocks = vi.hoisted(() => ({
  getIssueOrchestration: vi.fn(),
  getIssue: vi.fn(),
  listTasksByIssue: vi.fn(),
  listAgents: vi.fn(),
  listSquads: vi.fn(),
  respondToOrchestrationStep: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({
  api: {
    getIssueOrchestration: mocks.getIssueOrchestration,
    getIssue: mocks.getIssue,
    listTasksByIssue: mocks.listTasksByIssue,
    listAgents: mocks.listAgents,
    listSquads: mocks.listSquads,
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
});
