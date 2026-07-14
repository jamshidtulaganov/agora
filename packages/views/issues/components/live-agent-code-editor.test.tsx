// @vitest-environment jsdom

import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentTask } from "@agora/core/types";
import { renderWithI18n } from "../../test/i18n";

const mockState = vi.hoisted(() => ({
  snapshot: [] as unknown[],
  messages: [] as unknown[],
  mutateAsync: vi.fn(),
}));

vi.mock("@agora/core/issues/mutations", () => ({
  useCreateComment: () => ({ mutateAsync: mockState.mutateAsync }),
}));

vi.mock("@agora/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@agora/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (_type: string, id: string) =>
      ({ "agent-1": "Walt", "agent-2": "Gus" })[id] ?? "Unknown Agent",
    getActorInitials: (_type: string, id: string) =>
      ({ "agent-1": "WA", "agent-2": "GU" })[id] ?? "UA",
    getActorAvatarUrl: () => null,
  }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({
    actorType,
    actorId,
  }: {
    actorType: string;
    actorId: string;
  }) => (
    <span
      data-testid="actor-avatar"
      data-actor-type={actorType}
      data-actor-id={actorId}
    />
  ),
}));

vi.mock("@agora/core/chat/queries", () => ({
  taskMessagesOptions: (taskId: string) => ({
    queryKey: ["task-messages", taskId],
  }),
}));

vi.mock("@tanstack/react-query", async () => {
  const actual =
    await vi.importActual<typeof import("@tanstack/react-query")>(
      "@tanstack/react-query",
    );
  return {
    ...actual,
    useQuery: (opts: { queryKey?: readonly unknown[] }) => {
      if (opts.queryKey?.[2] === "agent-task-snapshot") {
        return { data: mockState.snapshot };
      }
      if (opts.queryKey?.[0] === "task-messages") {
        return { data: mockState.messages };
      }
      return { data: undefined };
    },
  };
});

import { LiveAgentCodeEditor } from "./live-agent-code-editor";

function makeTask(overrides: Partial<AgentTask>): AgentTask {
  return {
    id: "task-1",
    agent_id: "agent-1",
    runtime_id: "runtime-1",
    issue_id: "issue-1",
    status: "running",
    priority: 0,
    dispatched_at: null,
    started_at: "2026-07-08T08:00:00Z",
    completed_at: null,
    result: null,
    error: null,
    created_at: "2026-07-08T08:00:00Z",
    ...overrides,
  };
}

function writeMsg(seq: number, path: string, content: string) {
  return {
    seq,
    type: "tool_use",
    tool: "Write",
    input: { file_path: path, content },
    created_at: "2026-07-08T08:00:00Z",
  };
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  mockState.snapshot = [];
  mockState.messages = [];
});

describe("LiveAgentCodeEditor", () => {
  it("shows the idle empty state when no agent is running", () => {
    renderWithI18n(<LiveAgentCodeEditor issueId="issue-1" />);
    expect(screen.getByText("Nothing running yet")).toBeInTheDocument();
  });

  it("ignores terminal and unrelated tasks", () => {
    mockState.snapshot = [
      makeTask({ id: "t-done", status: "completed" }),
      makeTask({ id: "t-other", issue_id: "issue-2" }),
    ];
    renderWithI18n(<LiveAgentCodeEditor issueId="issue-1" />);
    expect(screen.getByText("Nothing running yet")).toBeInTheDocument();
  });

  it("shows the warming-up state for a run with no edits yet", () => {
    mockState.snapshot = [makeTask({})];
    renderWithI18n(<LiveAgentCodeEditor issueId="issue-1" />);
    expect(screen.getAllByText(/warming up/).length).toBeGreaterThan(0);
    expect(screen.getAllByText("Walt").length).toBeGreaterThan(0);
    expect(screen.getByText("Live")).toBeInTheDocument();
  });

  it("renders a written file as an editor buffer with tree, gutter, and agent cursor", () => {
    mockState.snapshot = [makeTask({})];
    mockState.messages = [
      writeMsg(1, "repo/src/dashboard/chart.tsx", "import { x } from 'y';\nexport const z = 1;"),
    ];
    renderWithI18n(<LiveAgentCodeEditor issueId="issue-1" />);

    // Tree: dir group + file entry; code pane: both lines; status bar: count.
    expect(screen.getByText("dashboard")).toBeInTheDocument();
    expect(screen.getAllByText("chart.tsx").length).toBeGreaterThan(0);
    expect(screen.getByText("import { x } from 'y';")).toBeInTheDocument();
    expect(screen.getByText("export const z = 1;")).toBeInTheDocument();
    expect(screen.getByText("1 file")).toBeInTheDocument();
    // Agent pill cursor (top chip + cursor pill both carry the name).
    expect(screen.getAllByText("Walt").length).toBeGreaterThan(1);
  });

  it("follows the newest file and lets the user pin + resume following", () => {
    mockState.snapshot = [makeTask({})];
    mockState.messages = [
      writeMsg(1, "repo/src/dashboard/filters.tsx", "old file"),
      writeMsg(2, "repo/src/dashboard/chart.tsx", "new file"),
    ];
    renderWithI18n(<LiveAgentCodeEditor issueId="issue-1" />);

    // Following the newest write: chart.tsx content visible.
    expect(screen.getByText("new file")).toBeInTheDocument();
    expect(screen.queryByText("Follow live")).not.toBeInTheDocument();

    // Pin the older file → its content shows and the follow affordance appears.
    fireEvent.click(screen.getByTitle("repo/src/dashboard/filters.tsx"));
    expect(screen.getByText("old file")).toBeInTheDocument();
    const follow = screen.getByText("Follow live");
    expect(follow).toBeInTheDocument();

    // Resume following → back to the live file.
    fireEvent.click(follow);
    expect(screen.getByText("new file")).toBeInTheDocument();
  });

  it("offers the jump to the full editor", () => {
    mockState.snapshot = [makeTask({})];
    mockState.messages = [writeMsg(1, "a/b.ts", "x")];
    const onOpen = vi.fn();
    renderWithI18n(
      <LiveAgentCodeEditor issueId="issue-1" onOpenFullEditor={onOpen} />,
    );
    fireEvent.click(screen.getByText("Open editor"));
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it("posts a PR-style line comment quoting file:line", async () => {
    mockState.snapshot = [makeTask({})];
    mockState.messages = [
      writeMsg(1, "repo/src/dashboard/chart.tsx", "const a = 1;\nconst b = 2;"),
    ];
    mockState.mutateAsync.mockResolvedValue({ id: "c-1" });
    renderWithI18n(<LiveAgentCodeEditor issueId="issue-1" />);

    // Gutter 💬 buttons exist for every numbered line (hover is CSS-only).
    const btns = screen.getAllByRole("button", {
      name: "Comment on this line",
    });
    expect(btns).toHaveLength(2);
    fireEvent.click(btns[1]!);

    fireEvent.change(
      screen.getByPlaceholderText(/Comment for the agent and team/),
      { target: { value: "rename b to total" } },
    );
    fireEvent.click(screen.getByRole("button", { name: "Send" }));

    expect(mockState.mutateAsync).toHaveBeenCalledTimes(1);
    const { content } = mockState.mutateAsync.mock.calls[0]![0] as {
      content: string;
    };
    expect(content).toContain(".../dashboard/chart.tsx:2");
    expect(content).toContain("> `const b = 2;`");
    expect(content).toContain("rename b to total");

    // Form closes after a successful post.
    await waitFor(() =>
      expect(
        screen.queryByPlaceholderText(/Comment for the agent and team/),
      ).not.toBeInTheDocument(),
    );
  });

  it("streams the step trail for a run that writes no files (QA-style)", () => {
    mockState.snapshot = [makeTask({})];
    mockState.messages = [
      { seq: 1, type: "tool_use", tool: "Bash", input: { command: "npm ci 2>&1 | tail -10" }, created_at: "2026-07-08T08:00:00Z" },
      { seq: 2, type: "tool_use", tool: "Read", input: { file_path: "repo/src/app.ts" }, created_at: "2026-07-08T08:00:01Z" },
      { seq: 3, type: "tool_use", tool: "Bash", input: { command: "npm run lint:check 2>&1" }, created_at: "2026-07-08T08:00:02Z" },
    ];
    renderWithI18n(<LiveAgentCodeEditor issueId="issue-1" />);

    // Still the waiting state (no file docs), but the trail streams the steps —
    // as human phrases, not raw shell (raw summary stays on the title attr).
    expect(screen.getAllByText(/warming up/).length).toBeGreaterThan(0);
    expect(screen.getByText("installing dependencies")).toBeInTheDocument();
    expect(screen.getByText("checking code style")).toBeInTheDocument();
    expect(screen.getByText(/is reading .*app\.ts/)).toBeInTheDocument();
    // Chronological top→down: install (oldest) renders before lint (newest).
    const items = screen.getAllByRole("listitem").map((li) => li.textContent);
    expect(items.indexOf("installing dependencies")).toBeLessThan(
      items.indexOf("checking code style"),
    );
  });

  it("renders the Uzbek live badge", () => {
    mockState.snapshot = [makeTask({})];
    renderWithI18n(<LiveAgentCodeEditor issueId="issue-1" />, {
      locale: "uz",
    });
    expect(screen.getByText("Jonli")).toBeInTheDocument();
  });
});
