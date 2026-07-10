import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { SprintDeployPanel, type SprintDeployPanelIssue } from "./sprint-deploy-panel";

// The sprint-level deploy panel (deploy cycle rehome, part 2): environments
// from project.settings.deploy_environments, the deploy slice-action fired
// against the sprint's anchor issue with the sprint branch as ref, the
// human-gate confirm for requires_human/production environments, and the
// deploy_event history read off the same anchor.

const apiMocks = vi.hoisted(() => ({
  getProject: vi.fn(),
  sliceAction: vi.fn(),
  getIssueDeployEvents: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));

const DEPLOY_ENVIRONMENTS = [
  {
    key: "staging",
    label: "Staging",
    kind: "gitlab_pipeline",
    target: { project_path: "salesdoctor/sd-main", ref: "staging" },
  },
  {
    key: "production",
    label: "Production",
    kind: "gitlab_pipeline",
    requires_human: true,
    target: { project_path: "salesdoctor/sd-main", ref: "main" },
  },
];

function project(settings: Record<string, unknown> = {}) {
  return {
    id: "project-1",
    workspace_id: "ws-1",
    title: "SD Main",
    description: null,
    icon: null,
    status: "in_progress",
    priority: "none",
    lead_type: null,
    lead_id: null,
    squad_id: null,
    settings,
    created_at: "",
    updated_at: "",
    issue_count: 0,
    done_count: 0,
    resource_count: 0,
  };
}

function deployEvent(over: Record<string, unknown> = {}) {
  return {
    id: "de-1",
    issue_id: "issue-2",
    ref: "sprint-9",
    target: "staging",
    status: "success",
    summary: "pipeline green",
    captured_at: "2026-07-01T00:00:00Z",
    ...over,
  };
}

const ISSUES: SprintDeployPanelIssue[] = [
  { id: "issue-1", number: 10, status: "in_review" },
  // Highest-numbered but cancelled — must NOT be the anchor.
  { id: "issue-3", number: 30, status: "cancelled" },
  { id: "issue-2", number: 20, status: "in_progress" },
];

function renderPanel(over: Partial<Parameters<typeof SprintDeployPanel>[0]> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={qc}>
        <SprintDeployPanel
          wsId="ws-1"
          projectId="project-1"
          sprintId="sprint-uuid-1"
          branch="sprint-9"
          issues={ISSUES}
          {...over}
        />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("SprintDeployPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.getProject.mockResolvedValue(project({ deploy_environments: DEPLOY_ENVIRONMENTS }));
    apiMocks.sliceAction.mockResolvedValue({ kind: "deploy", scope: "staging", instruction: "", agent_id: "a1", comment: {} });
    apiMocks.getIssueDeployEvents.mockResolvedValue({ latest: null, recent: [] });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the configured environments with deploy buttons and the sprint branch", async () => {
    renderPanel();
    await screen.findByText("Staging");
    expect(screen.getByText("Production")).toBeInTheDocument();
    expect(screen.getByTestId("sprint-deploy-staging")).toBeInTheDocument();
    expect(screen.getByTestId("sprint-deploy-production")).toBeInTheDocument();
    expect(screen.getByText("sprint-9")).toBeInTheDocument();
  });

  it("shows the compact empty state when the project configures no environments", async () => {
    apiMocks.getProject.mockResolvedValue(project({}));
    renderPanel();
    await screen.findByText(
      "No deploy environments configured — add deploy_environments in the project's settings.",
    );
    expect(screen.queryByTestId("sprint-deploy-staging")).not.toBeInTheDocument();
  });

  it("treats a malformed deploy_environments value as none configured (defensive parse)", async () => {
    apiMocks.getProject.mockResolvedValue(project({ deploy_environments: "garbage" }));
    renderPanel();
    await screen.findByText(
      "No deploy environments configured — add deploy_environments in the project's settings.",
    );
  });

  it("fires the deploy slice-action anchored to the highest-numbered non-cancelled issue, with the sprint branch as ref", async () => {
    renderPanel();
    fireEvent.click(await screen.findByTestId("sprint-deploy-staging"));
    await waitFor(() => expect(apiMocks.sliceAction).toHaveBeenCalledTimes(1));
    // issue-3 (#30) is cancelled -> the anchor is issue-2 (#20), not issue-1.
    expect(apiMocks.sliceAction).toHaveBeenCalledWith("issue-2", {
      kind: "deploy",
      scope: "staging",
      ref: "sprint-9",
    });
  });

  it("falls back to the sprint/<id> branch convention when the sprint has no explicit branch", async () => {
    renderPanel({ branch: "" });
    fireEvent.click(await screen.findByTestId("sprint-deploy-staging"));
    await waitFor(() => expect(apiMocks.sliceAction).toHaveBeenCalledTimes(1));
    expect(apiMocks.sliceAction).toHaveBeenCalledWith("issue-2", {
      kind: "deploy",
      scope: "staging",
      ref: "sprint/sprint-uuid-1",
    });
  });

  it("asks for confirmation before a human-gated (requires_human) deploy and aborts on cancel", async () => {
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(false);
    renderPanel();
    fireEvent.click(await screen.findByTestId("sprint-deploy-production"));
    expect(confirmSpy).toHaveBeenCalledTimes(1);
    expect(apiMocks.sliceAction).not.toHaveBeenCalled();
  });

  it("fires the human-gated deploy once confirmed", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    renderPanel();
    fireEvent.click(await screen.findByTestId("sprint-deploy-production"));
    await waitFor(() => expect(apiMocks.sliceAction).toHaveBeenCalledTimes(1));
    expect(apiMocks.sliceAction).toHaveBeenCalledWith("issue-2", {
      kind: "deploy",
      scope: "production",
      ref: "sprint-9",
    });
  });

  it("does not ask for confirmation on a non-gated environment", async () => {
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(false);
    renderPanel();
    fireEvent.click(await screen.findByTestId("sprint-deploy-staging"));
    await waitFor(() => expect(apiMocks.sliceAction).toHaveBeenCalledTimes(1));
    expect(confirmSpy).not.toHaveBeenCalled();
  });

  it("renders recent deploy history rows from the anchor issue's deploy events", async () => {
    apiMocks.getIssueDeployEvents.mockResolvedValue({
      latest: deployEvent(),
      recent: [
        deployEvent({ id: "de-2", ref: "sprint-9", status: "success" }),
        deployEvent({ id: "de-1", ref: "sprint-9", status: "failed", summary: "job build failed" }),
      ],
    });
    renderPanel();
    await screen.findByText("Success");
    expect(screen.getByText("Failed")).toBeInTheDocument();
    expect(screen.queryByText("No deploys yet.")).not.toBeInTheDocument();
    // History is read from the ANCHOR issue — the same issue deploys anchor to.
    expect(apiMocks.getIssueDeployEvents).toHaveBeenCalledWith("issue-2");
  });

  it("shows the history empty state when nothing has deployed yet", async () => {
    renderPanel();
    await screen.findByText("No deploys yet.");
  });

  it("disables deploy buttons and explains when every sprint issue is cancelled (no anchor)", async () => {
    renderPanel({ issues: [{ id: "issue-3", number: 30, status: "cancelled" }] });
    const btn = await screen.findByTestId("sprint-deploy-staging");
    expect(btn).toBeDisabled();
    expect(
      screen.getByText("No active task to anchor a deploy to — attach a task first."),
    ).toBeInTheDocument();
    expect(apiMocks.getIssueDeployEvents).not.toHaveBeenCalled();
  });
});
