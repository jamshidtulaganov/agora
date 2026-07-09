import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { DevLensBody } from "./dev-lens";
import type { EditorSession } from "./editor-workbench";

// DevLensBody mounts the co-code editor workbench as the cockpit's Dev stage
// (docs/sdlc-stage-cockpit-plan.md, phase F). These tests cover the lens's
// own gating — session states map to empty / error / loading / workbench —
// not the workbench internals (editor-workbench.test.tsx owns those), so the
// workbench module is stubbed wholesale, session included.

const apiMocks = vi.hoisted(() => ({
  getIssue: vi.fn(),
}));

const workbenchMocks = vi.hoisted(() => ({
  session: {} as Record<string, unknown>,
}));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("@agora/core", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("./editor-workbench", () => ({
  EditorWorkbench: () => <div data-testid="editor-workbench" />,
  useEditorSession: () => workbenchMocks.session,
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

function setSession(over: Partial<EditorSession> = {}) {
  workbenchMocks.session = {
    state: "ready",
    url: "/editor/proxy/tok/",
    err: "",
    agents: [],
    selectedId: null,
    daemon: null,
    selectedAgent: null,
    launch: vi.fn().mockResolvedValue(undefined),
    selectAgent: vi.fn().mockResolvedValue(undefined),
    ...over,
  };
}

function renderLens() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={qc}>
        <DevLensBody issueId="issue-1" />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("DevLensBody", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.getIssue.mockResolvedValue(baseIssue());
    setSession();
  });

  it("renders the workbench container once the session has an editor URL", async () => {
    renderLens();
    await screen.findByTestId("editor-workbench");
    expect(apiMocks.getIssue).toHaveBeenCalledWith("issue-1");
  });

  it("renders the empty state when the issue has no editor session (state none)", async () => {
    setSession({ state: "none", url: null });
    renderLens();

    await screen.findByText("No editor worktree yet.");
    expect(
      screen.getByText(
        "A worktree is created when an agent runs this issue. Assign an agent — the live editor opens here once it starts.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("editor-workbench")).not.toBeInTheDocument();
  });

  it("renders the launch spinner while the session is loading", async () => {
    setSession({ state: "loading", url: null });
    renderLens();

    await screen.findByText("Launching editor…");
    expect(screen.queryByTestId("editor-workbench")).not.toBeInTheDocument();
  });

  it("renders the error with a retry that re-launches the session", async () => {
    const launch = vi.fn().mockResolvedValue(undefined);
    setSession({ state: "error", url: null, err: "daemon launch failed (503)", launch });
    renderLens();

    await screen.findByText("daemon launch failed (503)");
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(launch).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId("editor-workbench")).not.toBeInTheDocument();
  });
});
