import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { DeployLensBody } from "./deploy-lens";

// DeployLensBody re-mounts EditorDeployQA (a standalone component, no editor
// context) plus a summary of the box bound to the issue's project
// (docs/sdlc-stage-cockpit-plan.md, phase E). This test covers the lens's own
// gating: no deploy target (feature off, or no box bound) -> empty state;
// a bound box -> box-info panel + the deploy action mounts.

const apiMocks = vi.hoisted(() => ({
  getIssue: vi.fn(),
  listRemoteBoxes: vi.fn(),
}));

let remoteBoxesEnabled = true;

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("@agora/core", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@agora/core/config", () => ({
  useConfigStore: (selector: (s: { remoteBoxesEnabled: boolean }) => unknown) =>
    selector({ remoteBoxesEnabled }),
}));
vi.mock("./editor-deploy-qa", () => ({
  EditorDeployQA: () => <div data-testid="editor-deploy-qa" />,
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
    metadata: {},
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

function renderLens() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={qc}>
        <DeployLensBody issueId="issue-1" />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("DeployLensBody", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    remoteBoxesEnabled = true;
    apiMocks.getIssue.mockResolvedValue(baseIssue());
    apiMocks.listRemoteBoxes.mockResolvedValue([]);
  });

  it("renders the empty state when the project has no bound box", async () => {
    renderLens();
    await screen.findByText("No deploy target bound to this project.");
    expect(screen.queryByTestId("editor-deploy-qa")).not.toBeInTheDocument();
  });

  it("renders the empty state when the remote-boxes feature is disabled, even with a bound box", async () => {
    remoteBoxesEnabled = false;
    apiMocks.listRemoteBoxes.mockResolvedValue([box()]);
    renderLens();
    await screen.findByText("No deploy target bound to this project.");
  });

  it("renders the bound box's info and the deploy action when a box is bound", async () => {
    apiMocks.listRemoteBoxes.mockResolvedValue([box()]);
    renderLens();

    await screen.findByTestId("editor-deploy-qa");
    expect(screen.getByText("jamshid's box")).toBeInTheDocument();
    expect(screen.getByText("jamshid.sdteam.uz")).toBeInTheDocument();
    expect(screen.getByText("online")).toBeInTheDocument();
    expect(screen.getByText("feature/foo")).toBeInTheDocument();
    expect(screen.queryByText("No deploy target bound to this project.")).not.toBeInTheDocument();
  });
});
