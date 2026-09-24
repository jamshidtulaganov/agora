import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import { ApiError } from "@agora/core/api";
import { NavigationProvider } from "../navigation";
import type { NavigationAdapter } from "../navigation";
import { RESOURCES } from "../locales";

const mockToastError = vi.hoisted(() => vi.fn());
const mockWorkspace = vi.hoisted(() => ({ id: "ws-1", slug: "acme", name: "Acme" }));
vi.mock("sonner", () => ({
  toast: { error: mockToastError, success: vi.fn() },
}));

vi.mock("@agora/core/paths", async () => {
  const actual = await vi.importActual<typeof import("@agora/core/paths")>("@agora/core/paths");
  return {
    ...actual,
    useCurrentWorkspace: () => mockWorkspace,
  };
});

const mockGetAvailability = vi.hoisted(() => vi.fn());
const mockGetSessions = vi.hoisted(() => vi.fn());
const mockGetMessages = vi.hoisted(() => vi.fn());
const mockGetRuns = vi.hoisted(() => vi.fn());
const mockCreateMutate = vi.hoisted(() => vi.fn());
const mockSendMutate = vi.hoisted(() => vi.fn());
const mockUpdateMutate = vi.hoisted(() => vi.fn());
const mockDeleteMutate = vi.hoisted(() => vi.fn());
const mockCancelMutate = vi.hoisted(() => vi.fn());
const mockSetActiveSession = vi.hoisted(() => vi.fn());
const mockGetArtifact = vi.hoisted(() => vi.fn());
const panelStoreState = vi.hoisted(() => ({}));
const assistantStoreState = vi.hoisted(() => ({
  activeSessionId: null as string | null,
  draftsBySession: {} as Record<string, {content: string; request_id: string; context?: {workspace_id: string | null; timezone?: string}}>,
  composerContextBySession: {} as Record<string, {workspace_id: string | null; project_id?: string | null; attachments?: {id: string; filename: string; size_bytes: number}[]}>,
}));

vi.mock("@agora/core/assistant", async () => {
  const { useSyncExternalStore } = await import("react");
  const listeners = new Set<() => void>();
  const setDraft = (sessionId: string, draft: {content: string; request_id: string; context?: {workspace_id: string | null; timezone?: string}} | null) => {

      assistantStoreState.draftsBySession = { ...assistantStoreState.draftsBySession };
      if (draft) assistantStoreState.draftsBySession[sessionId] = draft;
      else delete assistantStoreState.draftsBySession[sessionId];
      listeners.forEach((listener) => listener());
  };
  const setComposerContext = (sessionId: string, value: {workspace_id: string | null; project_id?: string | null; attachments?: {id: string; filename: string; size_bytes: number}[]} | null) => {
      assistantStoreState.composerContextBySession = {...assistantStoreState.composerContextBySession};
      if (value) assistantStoreState.composerContextBySession[sessionId] = value;
      else delete assistantStoreState.composerContextBySession[sessionId];
      listeners.forEach((listener) => listener());
    };
  const state = () => ({
    activeSessionId: assistantStoreState.activeSessionId,
    draftsBySession: assistantStoreState.draftsBySession,
    composerContextBySession: assistantStoreState.composerContextBySession,
    setActiveSession: mockSetActiveSession,
    setDraft,
    setComposerContext,
  });
  // Zustand stores are both callable (with a selector) and expose
  // .getState() — see CLAUDE.md testing conventions.
  const useAssistantStore = Object.assign(
    (selector?: (s: ReturnType<typeof state>) => unknown) =>
      useSyncExternalStore((listener) => { listeners.add(listener); return () => listeners.delete(listener); }, () => selector ? selector(state()) : assistantStoreState),
    { getState: state },
  );

  return {
    useAssistantStore,
    assistantAvailabilityOptions: () => ({
      queryKey: ["assistant", "availability"],
      queryFn: mockGetAvailability,
    }),
    assistantSessionListOptions: () => ({
      queryKey: ["assistant", "sessions"],
      queryFn: mockGetSessions,
    }),
    assistantSessionOptions: (id: string) => ({
      queryKey: ["assistant", "session", id],
      queryFn: () => Promise.resolve({ id }),
      enabled: !!id,
    }),
    assistantRunsOptions: (sessionId: string) => ({ queryKey: ["assistant", "runs", sessionId], queryFn: () => mockGetRuns(sessionId), enabled: !!sessionId }),
    assistantMessagesOptions: (sessionId: string) => ({
      queryKey: ["assistant", "messages", sessionId],
      queryFn: () => mockGetMessages(sessionId),
      enabled: !!sessionId,
    }),
    useCreateAssistantSession: () => ({ mutate: mockCreateMutate, isPending: false }),
    useUpdateAssistantSession: () => ({ mutate: mockUpdateMutate }),
    useDeleteAssistantSession: () => ({ mutate: mockDeleteMutate }),
    assistantArtifactOptions: (id: string) => ({
      queryKey: ["assistant", "artifact", id],
      queryFn: () => mockGetArtifact(id),
      enabled: !!id,
      retry: false,
    }),
    useAssistantPanelStore: Object.assign(
      (selector?: (s: typeof panelStoreState) => unknown) =>
        selector ? selector(panelStoreState) : panelStoreState,
      { getState: () => panelStoreState },
    ),
    useSendAssistantMessage: () => ({ mutateAsync: mockSendMutate, isPending: false }),
    useCancelAssistantRun: () => ({ mutate: mockCancelMutate }),
  };
});

vi.mock("./components/compose-resources", () => ({ useAssistantComposeResources: () => ({ toolbar: null, attachments: null, notices: null }) }));

import { AssistantPage } from "./assistant-page";

function renderPage() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const nav: NavigationAdapter = {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/assistant",
    searchParams: new URLSearchParams(),
    getShareableUrl: (p) => p,
  };
  return render(
    <I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={qc}>
        <NavigationProvider value={nav}>
          <AssistantPage />
        </NavigationProvider>
      </QueryClientProvider>
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  assistantStoreState.activeSessionId = null;
  assistantStoreState.draftsBySession = {};
  assistantStoreState.composerContextBySession = {};
  mockWorkspace.id = "ws-1";
  mockWorkspace.name = "Acme";
  mockGetMessages.mockResolvedValue([]);
  mockGetRuns.mockResolvedValue([]);
  mockSendMutate.mockResolvedValue({});
});

afterEach(() => {
  cleanup();
});

describe("AssistantPage — availability gate", () => {
  it("renders the not-configured state when the instance has the feature disabled", async () => {
    mockGetAvailability.mockResolvedValue({ enabled: false, model_label: "" });
    mockGetSessions.mockResolvedValue([]);

    renderPage();

    await screen.findByText("The Assistant isn't set up");
    expect(
      screen.queryByPlaceholderText("Message the Assistant..."),
    ).not.toBeInTheDocument();
    // Sessions are never fetched when the feature is off.
    expect(mockGetSessions).not.toHaveBeenCalled();
  });
});

describe("AssistantPage — empty state", () => {
  it("shows example prompts with no sessions, and clicking one prefills the composer", async () => {
    mockGetAvailability.mockResolvedValue({ enabled: true, model_label: "Agora" });
    mockGetSessions.mockResolvedValue([]);

    renderPage();

    await screen.findByText("Ask Agora anything");
    const promptButton = screen.getByText("What's on my plate across all my workspaces?");
    await userEvent.click(promptButton);

    const textarea = screen.getByPlaceholderText(
      "Message the Assistant...",
    ) as HTMLTextAreaElement;
    expect(textarea.value).toBe("What's on my plate across all my workspaces?");
    // Nothing is sent yet — picking a prompt only prefills.
    expect(mockCreateMutate).not.toHaveBeenCalled();
    expect(mockSendMutate).not.toHaveBeenCalled();
  });
});

describe("AssistantPage — sessions list", () => {
  it("renders sessions in the rail and switches the active session on click", async () => {
    mockGetAvailability.mockResolvedValue({ enabled: true, model_label: "Agora" });
    mockGetSessions.mockResolvedValue([
      {
        id: "s1",
        title: "Plan my week",
        focus_workspace_id: null,
        created_at: "2026-09-16T09:00:00Z",
        updated_at: "2026-09-16T09:00:00Z",
      },
      {
        id: "s2",
        title: "",
        focus_workspace_id: null,
        created_at: "2026-09-15T09:00:00Z",
        updated_at: "2026-09-15T09:00:00Z",
      },
    ]);
    assistantStoreState.activeSessionId = "s1";

    renderPage();

    await screen.findByText("Plan my week");
    // "New chat" is both the rail's create button and the untitled row's
    // label; the row is the one that is not the create button.
    const untitledRow = screen
      .getAllByText("New chat")
      .find((el) => !el.closest("button")?.querySelector("svg.lucide-plus"));
    if (!untitledRow) throw new Error("untitled session row not rendered");
    await userEvent.click(untitledRow);

    expect(mockSetActiveSession).toHaveBeenCalledWith("s2");
  });
});

describe("AssistantPage — send flow", () => {
  it("sends a message on the active session when Enter is pressed", async () => {
    mockGetAvailability.mockResolvedValue({ enabled: true, model_label: "Agora" });
    mockGetSessions.mockResolvedValue([
      {
        id: "s1",
        title: "Chat",
        focus_workspace_id: null,
        created_at: "2026-09-16T09:00:00Z",
        updated_at: "2026-09-16T09:00:00Z",
      },
    ]);
    assistantStoreState.activeSessionId = "s1";

    renderPage();

    const textarea = await screen.findByPlaceholderText("Message the Assistant...");
    await userEvent.type(textarea, "What is on my plate?");
    await userEvent.keyboard("{Enter}");

    await waitFor(() => {
      expect(mockSendMutate).toHaveBeenCalledWith(expect.objectContaining({
        content: "What is on my plate?",
        request_id: expect.any(String),
        context: expect.objectContaining({ workspace_id: "ws-1" }),
      }));
    });
  });

  it("does not send on Shift+Enter (inserts a newline instead)", async () => {
    mockGetAvailability.mockResolvedValue({ enabled: true, model_label: "Agora" });
    mockGetSessions.mockResolvedValue([
      {
        id: "s1",
        title: "Chat",
        focus_workspace_id: null,
        created_at: "2026-09-16T09:00:00Z",
        updated_at: "2026-09-16T09:00:00Z",
      },
    ]);
    assistantStoreState.activeSessionId = "s1";

    renderPage();

    // Wait for the settled launcher (messages query resolved → empty state).
    // The composer REMOUNTS when the loading branch switches to the launcher,
    // so grabbing the textarea before that leaves the test typing into a
    // detached node.
    await screen.findByText("Ask Agora anything");
    const textarea = await screen.findByPlaceholderText("Message the Assistant...");
    await userEvent.type(textarea, "line one{Shift>}{Enter}{/Shift}line two");

    expect(mockSendMutate).not.toHaveBeenCalled();
    expect((textarea as HTMLTextAreaElement).value).toBe("line one\nline two");
  });

  it("keeps a failed send with its request id and captured workspace for retry", async () => {
    mockGetAvailability.mockResolvedValue({ enabled: true, model_label: "Agora" });
    mockGetSessions.mockResolvedValue([{ id: "s1", title: "Chat", focus_workspace_id: "ws-old", created_at: "2026-09-16T09:00:00Z", updated_at: "2026-09-16T09:00:00Z" }]);
    assistantStoreState.activeSessionId = "s1";
    mockSendMutate.mockRejectedValueOnce(new Error("network"));

    renderPage();
    await screen.findByText("Ask Agora anything");
    const textarea = screen.getByPlaceholderText("Message the Assistant...");
    await userEvent.type(textarea, "Create an issue");
    await userEvent.keyboard("{Enter}");
    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith("Failed to send message"));

    expect(textarea).toHaveValue("Create an issue");
    const first = mockSendMutate.mock.calls[0]![0];
    expect(first.context.workspace_id).toBe("ws-1");
    expect(first.request_id).toEqual(expect.any(String));

    await userEvent.keyboard("{Enter}");
    await waitFor(() => expect(mockSendMutate).toHaveBeenCalledTimes(2));
    expect(mockSendMutate.mock.calls[1]![0]).toEqual(first);
  });

  it("surfaces a distinct toast when the server reports a run already in progress (409)", async () => {
    mockGetAvailability.mockResolvedValue({ enabled: true, model_label: "Agora" });
    mockGetSessions.mockResolvedValue([
      {
        id: "s1",
        title: "Chat",
        focus_workspace_id: null,
        created_at: "2026-09-16T09:00:00Z",
        updated_at: "2026-09-16T09:00:00Z",
      },
    ]);
    assistantStoreState.activeSessionId = "s1";
    mockSendMutate.mockRejectedValue(new ApiError("conflict", 409, "Conflict"));

    renderPage();

    const textarea = await screen.findByPlaceholderText("Message the Assistant...");
    await userEvent.type(textarea, "hello");
    await userEvent.keyboard("{Enter}");

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith(
        "The Assistant is still answering your last message",
      );
    });
  });

  it("keeps the composer editable and offers Stop while a run is active", async () => {
    mockGetAvailability.mockResolvedValue({ enabled: true, model_label: "Agora" });
    mockGetSessions.mockResolvedValue([
      {
        id: "s1",
        title: "Chat",
        focus_workspace_id: null,
        created_at: "2026-09-16T09:00:00Z",
        updated_at: "2026-09-16T09:00:00Z",
      },
    ]);
    assistantStoreState.activeSessionId = "s1";
    mockGetRuns.mockResolvedValue([{ id: "run-1", status: "running", active_tool: null }]);

    renderPage();

    const textarea = (await screen.findByPlaceholderText(
      "Message the Assistant...",
    )) as HTMLTextAreaElement;
    expect(textarea.disabled).toBe(false);

    const stopButton = await screen.findByRole("button", { name: "Stop" });
    await userEvent.click(stopButton);
    expect(mockCancelMutate).toHaveBeenCalledWith("run-1");
  });

  it("shows a failed run with recovery guidance while retaining the transcript", async () => {
    mockGetAvailability.mockResolvedValue({ enabled: true, model_label: "Agora" });
    mockGetSessions.mockResolvedValue([{ id: "s1", title: "Chat", focus_workspace_id: null, created_at: "2026-09-16T09:00:00Z", updated_at: "2026-09-16T09:00:00Z" }]);
    mockGetMessages.mockResolvedValue([{ id: "m1", session_id: "s1", role: "assistant", content: "I created an issue.", created_at: "2026-09-16T09:00:00Z" }]);
    mockGetRuns.mockResolvedValue([{ id: "run-1", status: "failed", active_tool: null }]);
    assistantStoreState.activeSessionId = "s1";

    renderPage();
    expect(await screen.findByText("I created an issue.")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("The Assistant could not finish this reply");
    expect(screen.getByRole("status")).toHaveTextContent("Some changes may already have been applied");
  });
});

// Artifacts render inline in the transcript (components/inline-artifact.tsx);
// the page has no side pane any more.
describe("AssistantPage — inline artifacts", () => {
  it("shows the artifact in the conversation, with no side pane", async () => {
    mockGetAvailability.mockResolvedValue({ enabled: true, model_label: "Agora" });
    mockGetSessions.mockResolvedValue([
      {
        id: "session-1",
        title: "Usage",
        focus_workspace_id: null,
        created_at: "2026-09-16T10:00:00Z",
        updated_at: "2026-09-16T10:00:00Z",
      },
    ]);
    assistantStoreState.activeSessionId = "session-1";
    mockGetMessages.mockResolvedValue([
      {
        id: "m1",
        session_id: "session-1",
        role: "tool",
        content: "{}",
        tool_name: "create_artifact",
        tool_result: { artifact_id: "art-1", title: "Weekly digest", kind: "markdown", version: 1 },
        created_at: "2026-09-16T10:00:00Z",
      },
    ]);
    mockGetArtifact.mockResolvedValue({
      id: "art-1",
      session_id: "session-1",
      title: "Weekly digest",
      kind: "markdown",
      content: "Three issues closed.",
      version: 1,
      created_at: "2026-09-16T10:00:00Z",
      updated_at: "2026-09-16T10:00:00Z",
    });

    renderPage();

    expect(await screen.findByText("Three issues closed.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Print or save as PDF" })).toBeInTheDocument();
    expect(screen.queryByRole("complementary")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Close artifact" })).not.toBeInTheDocument();
  });
});

describe("AssistantPage — narrow-width rail", () => {
  const sessions = [
    {
      id: "s1",
      title: "Plan my week",
      focus_workspace_id: null,
      created_at: "2026-09-16T09:00:00Z",
      updated_at: "2026-09-16T09:00:00Z",
    },
    {
      id: "s2",
      title: "Usage",
      focus_workspace_id: null,
      created_at: "2026-09-15T09:00:00Z",
      updated_at: "2026-09-15T09:00:00Z",
    },
  ];

  beforeEach(() => {
    mockGetAvailability.mockResolvedValue({ enabled: true, model_label: "Agora" });
    mockGetSessions.mockResolvedValue(sessions);
    assistantStoreState.activeSessionId = "s1";
  });

  it("toggles the rail from the header and closes it again on pick", async () => {
    renderPage();

    const toggle = await screen.findByRole("button", { name: "Show chats" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");

    await userEvent.click(toggle);
    expect(await screen.findByRole("button", { name: "Hide chats" })).toHaveAttribute(
      "aria-expanded",
      "true",
    );

    await userEvent.click(screen.getByText("Usage"));

    expect(mockSetActiveSession).toHaveBeenCalledWith("s2");
    expect(await screen.findByRole("button", { name: "Show chats" })).toBeInTheDocument();
  });

  it("starts a new chat on Cmd/Ctrl+Shift+O (Cmd+K belongs to global search)", async () => {
    renderPage();
    await screen.findByText("Plan my week");

    fireEvent.keyDown(document, { key: "O", code: "KeyO", metaKey: true, shiftKey: true });
    expect(mockCreateMutate).toHaveBeenCalledTimes(1);

    fireEvent.keyDown(document, { key: "o", code: "KeyO", ctrlKey: true, shiftKey: true });
    expect(mockCreateMutate).toHaveBeenCalledTimes(2);

    // Bare ⌘K stays out of it.
    fireEvent.keyDown(document, { key: "k", code: "KeyK", metaKey: true });
    expect(mockCreateMutate).toHaveBeenCalledTimes(2);
  });
});
