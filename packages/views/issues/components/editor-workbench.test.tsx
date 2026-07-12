import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { EditorWorkbench, type EditorSession } from "./editor-workbench";

// EditorWorkbench is the editor Dialog's inner surface extracted from
// editor-section.tsx so the cockpit's Dev lens can mount it without the
// Dialog (docs/sdlc-stage-cockpit-plan.md, phase F). These tests cover the
// workbench's OWN composition — the pane switcher swapping the mounted pane,
// the right-rail tabs, and the controlled-pane contract the Dialog host
// relies on. The heavy panes (code-server iframe aside) are stubbed: their
// internals have their own coverage (live-agent-code-editor.test.tsx,
// editor-preview-pane.test.tsx).

const apiMocks = vi.hoisted(() => ({
  getAgentTaskSnapshot: vi.fn(),
  getProject: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));

vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("./live-agent-code-editor", () => ({
  LiveAgentCodeEditor: () => <div data-testid="live-agent-editor" />,
}));
vi.mock("./editor-preview-pane", () => ({
  EditorPreviewPane: () => <div data-testid="preview-pane" />,
  parseTestOutput: () => ({ failed: [], failedCount: 0, passedCount: 0 }),
}));
vi.mock("./editor-browser-pane", () => ({
  EditorBrowserPane: () => <div data-testid="browser-pane" />,
}));
vi.mock("./editor-chat-panel", () => ({
  EditorChatPanel: () => <div data-testid="chat-panel" />,
}));
vi.mock("./editor-context-panel", () => ({
  EditorContextPanel: () => <div data-testid="context-panel" />,
}));
vi.mock("./editor-changes-list", () => ({ EditorChangesList: () => null }));
vi.mock("./live-agent-changes-feed", () => ({ LiveAgentChangesFeed: () => null }));
vi.mock("./agent-working-indicator", () => ({ AgentWorkingIndicator: () => null }));
vi.mock("./editor-review-bar", () => ({
  EditorReviewBar: () => <div data-testid="review-bar" />,
}));
vi.mock("./editor-ask-bar", () => ({
  EditorAskBar: () => <div data-testid="ask-bar" />,
}));
vi.mock("./editor-run-qa", () => ({
  EditorRunQA: () => <div data-testid="run-qa" />,
}));

function agent(over: Record<string, unknown> = {}) {
  return {
    agent_id: "agent-1",
    agent_name: "coder",
    work_dir: "/work/agent-1",
    status: "done",
    ...over,
  };
}

function makeSession(over: Partial<EditorSession> = {}): EditorSession {
  const a = agent();
  return {
    state: "ready",
    url: "/editor/proxy/tok/",
    err: "",
    agents: [a],
    selectedId: a.agent_id,
    daemon: { url: "/daemon-proxy", userId: "u1" },
    selectedAgent: a,
    launch: vi.fn().mockResolvedValue(undefined),
    selectAgent: vi.fn().mockResolvedValue(undefined),
    ...over,
  };
}

function renderWorkbench(props: Partial<Parameters<typeof EditorWorkbench>[0]> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <EditorWorkbench
        issueId="issue-1"
        issueKey="MUL-1"
        issueTitle="Fix onboarding"
        projectId="project-1"
        session={makeSession()}
        {...props}
      />
    </QueryClientProvider>,
  );
}

describe("EditorWorkbench", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.getAgentTaskSnapshot.mockResolvedValue([]);
    apiMocks.getProject.mockResolvedValue({ id: "project-1", settings: {} });
  });

  it("renders the pane switcher, ask bar, agent chips, and the code iframe by default", () => {
    renderWorkbench();

    for (const name of ["Live", "Code", "Preview", "Browser"]) {
      expect(screen.getByRole("button", { name })).toBeInTheDocument();
    }
    expect(screen.getByTestId("ask-bar")).toBeInTheDocument();
    expect(screen.getByTestId("review-bar")).toBeInTheDocument();
    expect(screen.getByText("coder")).toBeInTheDocument();
    // Default pane is Code → the code-server iframe mounts.
    expect(screen.getByTitle("code editor (expanded)")).toBeInTheDocument();
    expect(screen.queryByTestId("preview-pane")).not.toBeInTheDocument();
  });

  it("switching panes swaps the mounted pane; the code iframe stays mounted (hidden)", () => {
    renderWorkbench();

    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    expect(screen.getByTestId("preview-pane")).toBeInTheDocument();
    // Code-server never unmounts on a pane switch — switching back must not
    // reload VS Code.
    expect(screen.getByTitle("code editor (expanded)")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Browser" }));
    expect(screen.getByTestId("browser-pane")).toBeInTheDocument();
    expect(screen.queryByTestId("preview-pane")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Live" }));
    expect(screen.getByTestId("live-agent-editor")).toBeInTheDocument();
    expect(screen.queryByTestId("browser-pane")).not.toBeInTheDocument();
  });

  it("supports a controlled left pane (the Dialog host's contract)", () => {
    const onLeftPaneChange = vi.fn();
    renderWorkbench({ leftPane: "preview", onLeftPaneChange });

    expect(screen.getByTestId("preview-pane")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Browser" }));
    expect(onLeftPaneChange).toHaveBeenCalledWith("browser");
    // Controlled: the pane doesn't move until the host re-renders with the
    // new value.
    expect(screen.getByTestId("preview-pane")).toBeInTheDocument();
  });

  it("switches the right rail between Activity, Chat, and Context", () => {
    renderWorkbench();

    expect(screen.queryByTestId("chat-panel")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Chat" }));
    expect(screen.getByTestId("chat-panel")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Context" }));
    expect(screen.getByTestId("context-panel")).toBeInTheDocument();
    expect(screen.queryByTestId("chat-panel")).not.toBeInTheDocument();
  });

  it("renders custom actions and headerEnd when the Dialog host provides them", () => {
    renderWorkbench({
      actions: <div data-testid="host-actions" />,
      headerEnd: <button type="button" data-testid="host-close" />,
    });

    expect(screen.getByTestId("host-actions")).toBeInTheDocument();
    expect(screen.getByTestId("host-close")).toBeInTheDocument();
    // The default actions row is replaced, not duplicated.
    expect(screen.queryByTestId("run-qa")).not.toBeInTheDocument();
  });

  it("falls back to its own QA/deploy actions when the host passes none (lens mode)", () => {
    renderWorkbench();
    expect(screen.getByTestId("run-qa")).toBeInTheDocument();
  });
});
