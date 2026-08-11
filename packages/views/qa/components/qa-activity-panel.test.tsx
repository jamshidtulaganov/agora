import { forwardRef, useImperativeHandle, useRef, type Ref } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import type { TimelineEntry } from "@agora/core/types";
import { renderWithI18n } from "../../test/i18n";
import { QAActivityPanel } from "./qa-activity-panel";

// QAActivityPanel re-homes the issue conversation into the QA lens
// (stage-cockpit phase G). These tests cover the panel's OWN composition —
// the shared-timeline feed, the origin tabs, and the composer wiring into the
// create-comment path — not the already-tested building blocks (CommentCard,
// ResolvedThreadBar, the rich editor), which are stubbed so this file stays a
// focused unit test of the panel itself.

const apiMocks = vi.hoisted(() => ({
  listTimeline: vi.fn(),
  createComment: vi.fn(),
  previewCommentTriggers: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

// No WS provider in jsdom — the handlers are the live-update path, which the
// timeline hook's own tests cover.
vi.mock("@agora/core/realtime", () => ({
  useWSEvent: vi.fn(),
  useWSReconnect: vi.fn(),
}));

// Zustand store mock: callable selector + getState (stores are both).
const authState = vi.hoisted(() => ({
  user: { id: "user-1", name: "Alice", email: "alice@example.com" },
}));
vi.mock("@agora/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector: (s: typeof authState) => unknown) => selector(authState),
    { getState: () => authState },
  ),
}));

vi.mock("@agora/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (_type: string, id: string) =>
      id === "agent-1" ? "QA Bot" : "Alice",
  }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <span data-testid="actor-avatar" />,
}));

// Thread rendering has its own coverage (comment-card.test.tsx); here a stub
// that surfaces content + reply count is enough to assert the panel's
// grouping and filtering.
vi.mock("../../issues/components/comment-card", () => ({
  CommentCard: ({ entry, replies }: { entry: TimelineEntry; replies: TimelineEntry[] }) => (
    <div data-testid="comment-card" data-replies={replies.length}>
      {entry.content}
    </div>
  ),
}));
vi.mock("../../issues/components/resolved-thread-bar", () => ({
  ResolvedThreadBar: ({ entry }: { entry: TimelineEntry }) => (
    <div data-testid="resolved-bar">{entry.content}</div>
  ),
}));

vi.mock("@agora/core/hooks/use-file-upload", () => ({
  useFileUpload: () => ({ uploadWithToast: vi.fn() }),
}));

// The rich editor doesn't run in jsdom — same textarea stand-in as
// comment-composers.test.tsx, exposing the ContentEditorRef surface the
// composer drives.
vi.mock("../../editor", () => ({
  useFileDropZone: () => ({
    isDragOver: false,
    dropZoneProps: { "data-testid": "drop-zone" },
  }),
  FileDropOverlay: () => null,
  ContentEditor: forwardRef(function MockContentEditor(
    {
      defaultValue,
      onUpdate,
      placeholder,
    }: {
      defaultValue?: string;
      onUpdate?: (markdown: string) => void;
      placeholder?: string;
    },
    ref: Ref<unknown>,
  ) {
    const valueRef = useRef(defaultValue ?? "");
    useImperativeHandle(ref, () => ({
      getMarkdown: () => valueRef.current,
      clearContent: () => {
        valueRef.current = "";
      },
      focus: () => {},
      blur: () => {},
      uploadFile: async () => {},
      hasActiveUploads: () => false,
    }));
    return (
      <textarea
        data-testid="editor"
        defaultValue={defaultValue}
        placeholder={placeholder}
        onChange={(event) => {
          valueRef.current = event.target.value;
          onUpdate?.(event.target.value);
        }}
      />
    );
  }),
}));

function entry(over: Partial<TimelineEntry> & { id: string }): TimelineEntry {
  return {
    type: "comment",
    actor_type: "member",
    actor_id: "user-1",
    content: "",
    parent_id: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    comment_type: "comment",
    reactions: [],
    attachments: [],
    ...over,
  };
}

const humanComment = entry({
  id: "c-human",
  content: "Human repro note",
  created_at: "2026-01-01T00:01:00Z",
});
const agentComment = entry({
  id: "c-agent",
  actor_type: "agent",
  actor_id: "agent-1",
  content: "Agent QA report",
  created_at: "2026-01-01T00:02:00Z",
});
const agentReply = entry({
  id: "c-agent-reply",
  actor_type: "member",
  actor_id: "user-1",
  parent_id: "c-agent",
  content: "Reply riding the agent thread",
  created_at: "2026-01-01T00:03:00Z",
});
const statusActivity = entry({
  id: "a-status",
  type: "activity",
  action: "status_changed",
  details: { from: "in_progress", to: "in_review" },
  content: undefined,
  created_at: "2026-01-01T00:04:00Z",
});

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <QAActivityPanel issueId="issue-1" />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  apiMocks.listTimeline.mockResolvedValue([
    humanComment,
    agentComment,
    agentReply,
    statusActivity,
  ]);
  apiMocks.previewCommentTriggers.mockResolvedValue({ agents: [] });
  apiMocks.createComment.mockResolvedValue({
    id: "c-new",
    issue_id: "issue-1",
    author_type: "member",
    author_id: "user-1",
    content: "hello from qa",
    type: "comment",
    parent_id: null,
    reactions: [],
    attachments: [],
    created_at: "2026-01-01T00:05:00Z",
    updated_at: "2026-01-01T00:05:00Z",
  });
});

describe("QAActivityPanel", () => {
  it("renders comments, threads, and activity rows from the shared timeline query", async () => {
    renderPanel();

    // Both thread roots render through the (stubbed) CommentCard...
    await screen.findByText("Human repro note");
    expect(screen.getByText("Agent QA report")).toBeInTheDocument();
    // ...the reply rides its root thread instead of rendering standalone...
    const cards = screen.getAllByTestId("comment-card");
    expect(cards).toHaveLength(2);
    expect(
      cards.find((c) => c.textContent?.includes("Agent QA report")),
    ).toHaveAttribute("data-replies", "1");
    expect(screen.queryByText("Reply riding the agent thread")).toBeNull();
    // ...and the activity entry renders as a compact formatted event line.
    expect(screen.getByText(/changed status/)).toBeInTheDocument();
    // Single shared query key — the same fetch issue-detail uses.
    expect(apiMocks.listTimeline).toHaveBeenCalledWith("issue-1");
  });

  it("filters to agent-rooted threads on the Agents tab", async () => {
    renderPanel();
    await screen.findByText("Human repro note");

    fireEvent.click(screen.getByRole("tab", { name: /Agents/ }));

    expect(screen.getByText("Agent QA report")).toBeInTheDocument();
    expect(screen.queryByText("Human repro note")).toBeNull();
    // The people-authored activity entry classifies out of the agents tab too.
    expect(screen.queryByText(/changed status/)).toBeNull();
  });

  it("submits the composer through the create-comment path", async () => {
    renderPanel();
    await screen.findByText("Human repro note");

    fireEvent.change(screen.getByTestId("editor"), {
      target: { value: "hello from qa" },
    });
    const composer = screen.getByTestId("drop-zone");
    const buttons = within(composer).getAllByRole("button");
    fireEvent.click(buttons[buttons.length - 1]!);

    await waitFor(() =>
      expect(apiMocks.createComment).toHaveBeenCalledWith(
        "issue-1",
        "hello from qa",
        undefined,
        undefined,
        undefined,
        undefined,
        "auto",
      ),
    );
  });
});
