import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { act, render, screen, cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { toast } from "sonner";
import { I18nProvider } from "@agora/core/i18n/react";
import { NavigationProvider } from "../../navigation";
import type { NavigationAdapter } from "../../navigation";
import { RESOURCES } from "../../locales";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

vi.mock("@agora/core/paths", async () => {
  const actual = await vi.importActual<typeof import("@agora/core/paths")>("@agora/core/paths");
  return {
    ...actual,
    useCurrentWorkspace: () => ({ id: "ws-1", slug: "acme", name: "Acme" }),
    useWorkspaceSlug: () => "acme",
  };
});

const mockGetAvailability = vi.hoisted(() => vi.fn());
const mockGetSessions = vi.hoisted(() => vi.fn());
const mockGetMessages = vi.hoisted(() => vi.fn());
const mockGetRuns = vi.hoisted(() => vi.fn());
const mockCreateMutate = vi.hoisted(() => vi.fn());
const mockSendMutate = vi.hoisted(() => vi.fn());
const mockCancelMutate = vi.hoisted(() => vi.fn());
const mockUpdateMutate = vi.hoisted(() => vi.fn());
const mockSetActiveSession = vi.hoisted(() => vi.fn());
const mockPush = vi.hoisted(() => vi.fn());
const mockSetOpen = vi.hoisted(() => vi.fn());
const mockToggle = vi.hoisted(() => vi.fn());
const mockSetPanelSize = vi.hoisted(() => vi.fn());
const mockSetExpanded = vi.hoisted(() => vi.fn());
const mockMarkUnseenResult = vi.hoisted(() => vi.fn());
const mockClearUnseenResult = vi.hoisted(() => vi.fn());

// The FAB subscribes to assistant:run_finished for its unseen dot. Capture the
// handler so a test can deliver an event without a live WebSocket.
const wsHandlers = vi.hoisted(() => new Map<string, (payload: unknown) => void>());
vi.mock("@agora/core/realtime", () => ({
  useWSEvent: (event: string, handler: (payload: unknown) => void) => {
    wsHandlers.set(event, handler);
  },
}));

const mockSetOpenArtifact = vi.hoisted(() => vi.fn());
const assistantStoreState = vi.hoisted(() => ({
  activeSessionId: null as string | null,
  draftsBySession: {} as Record<string, {content: string; request_id: string; context?: {workspace_id: string | null; timezone?: string}}>,
  openArtifactId: {} as Record<string, string>,
}));

const panelStoreState = vi.hoisted(() => ({
  isOpen: false,
  panelWidth: 380,
  panelHeight: 600,
  isExpanded: false,
  hasUnseenResult: false,
}));

// Zustand stores are both callable (with a selector) and expose .getState() —
// see CLAUDE.md testing conventions.
vi.mock("@agora/core/assistant", async () => {
  const { useSyncExternalStore } = await import("react");
  const listeners = new Set<() => void>();
  const setDraft = (sessionId: string, draft: {content: string; request_id: string; context?: {workspace_id: string | null; timezone?: string}} | null) => {

      assistantStoreState.draftsBySession = { ...assistantStoreState.draftsBySession };
      if (draft) assistantStoreState.draftsBySession[sessionId] = draft;
      else delete assistantStoreState.draftsBySession[sessionId];
      listeners.forEach((listener) => listener());
  };
  const state = () => ({
    activeSessionId: assistantStoreState.activeSessionId,
    draftsBySession: assistantStoreState.draftsBySession,
    openArtifactId: assistantStoreState.openArtifactId,
    setActiveSession: mockSetActiveSession,
    setDraft,
    setOpenArtifact: mockSetOpenArtifact,
  });
  const useAssistantStore = Object.assign(
    (selector?: (s: ReturnType<typeof state>) => unknown) =>
      useSyncExternalStore((listener) => { listeners.add(listener); return () => listeners.delete(listener); }, () => selector ? selector(state()) : assistantStoreState),
    { getState: state },
  );

  const panelState = () => ({
    isOpen: panelStoreState.isOpen,
    panelWidth: panelStoreState.panelWidth,
    panelHeight: panelStoreState.panelHeight,
    isExpanded: panelStoreState.isExpanded,
    hasUnseenResult: panelStoreState.hasUnseenResult,
    setOpen: mockSetOpen,
    toggle: mockToggle,
    setPanelSize: mockSetPanelSize,
    setExpanded: mockSetExpanded,
    markUnseenResult: mockMarkUnseenResult,
    clearUnseenResult: mockClearUnseenResult,
  });
  const useAssistantPanelStore = Object.assign(
    (selector?: (s: ReturnType<typeof panelState>) => unknown) =>
      selector ? selector(panelState()) : panelState(),
    { getState: panelState },
  );

  return {
    useAssistantStore,
    useAssistantPanelStore,
    ASSISTANT_PANEL_MIN_W: 360,
    ASSISTANT_PANEL_MIN_H: 480,
    ASSISTANT_PANEL_DEFAULT_W: 380,
    ASSISTANT_PANEL_DEFAULT_H: 600,
    assistantAvailabilityOptions: () => ({
      queryKey: ["assistant", "availability"],
      queryFn: mockGetAvailability,
    }),
    assistantSessionListOptions: () => ({
      queryKey: ["assistant", "sessions"],
      queryFn: mockGetSessions,
    }),
    assistantRunsOptions: (sessionId: string) => ({ queryKey: ["assistant", "runs", sessionId], queryFn: () => mockGetRuns(sessionId), enabled: !!sessionId }),
    assistantMessagesOptions: (sessionId: string) => ({
      queryKey: ["assistant", "messages", sessionId],
      queryFn: () => mockGetMessages(sessionId),
      enabled: !!sessionId,
    }),
    useCreateAssistantSession: () => ({ mutate: mockCreateMutate, isPending: false }),
    useSendAssistantMessage: () => ({ mutateAsync: mockSendMutate, isPending: false }),
    useCancelAssistantRun: () => ({ mutate: mockCancelMutate }),
    // Used by the composer's context chip (the session's focus workspace).
    useUpdateAssistantSession: () => ({ mutate: mockUpdateMutate, isPending: false }),
  };
});

import { AssistantPanel } from "./assistant-panel";
import { AssistantFab } from "./assistant-fab";

function renderWithShell(ui: React.ReactElement, pathname = "/acme/issues") {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  // The composer's context chip resolves the session's focus workspace from
  // this cache (it never fetches one of its own).
  qc.setQueryData(["workspaces", "list"], [{ id: "ws-1", slug: "acme", name: "Acme" }]);
  const nav: NavigationAdapter = {
    push: mockPush,
    replace: vi.fn(),
    back: vi.fn(),
    pathname,
    searchParams: new URLSearchParams(),
    getShareableUrl: (p) => p,
  };
  return render(
    <I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={qc}>
        <NavigationProvider value={nav}>{ui}</NavigationProvider>
      </QueryClientProvider>
    </I18nProvider>,
  );
}


beforeEach(() => {
  vi.clearAllMocks();
  assistantStoreState.activeSessionId = null;
  assistantStoreState.draftsBySession = {};
  assistantStoreState.openArtifactId = {};
  panelStoreState.isOpen = false;
  panelStoreState.isExpanded = false;
  panelStoreState.hasUnseenResult = false;
  wsHandlers.clear();
  mockGetAvailability.mockResolvedValue({ enabled: true, model_label: "Agora" });
  mockGetSessions.mockResolvedValue([]);
  mockGetMessages.mockResolvedValue([]);
  mockGetRuns.mockResolvedValue([]);
  mockSendMutate.mockResolvedValue({});
});

afterEach(() => {
  cleanup();
});

describe("AssistantPanel — open / close", () => {
  it("renders nothing while the panel is closed", async () => {
    renderWithShell(<AssistantPanel />);

    await vi.waitFor(() => expect(mockGetAvailability).toHaveBeenCalled());
    expect(screen.queryByText("Assistant")).not.toBeInTheDocument();
    // A closed panel must not pull the session list either.
    expect(mockGetSessions).not.toHaveBeenCalled();
  });

  it("renders the header and the launcher when open with no session", async () => {
    panelStoreState.isOpen = true;

    renderWithShell(<AssistantPanel />);

    await screen.findByText("Assistant");
    expect(screen.getByRole("button", { name: "New chat" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Open full page" })).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Message the Assistant...")).toBeInTheDocument();
  });

  it("closes the panel from the header close button", async () => {
    panelStoreState.isOpen = true;

    renderWithShell(<AssistantPanel />);

    const closeButton = await screen.findByRole("button", { name: "Close" });
    await userEvent.click(closeButton);

    expect(mockSetOpen).toHaveBeenCalledWith(false);
  });

  it("navigates to the full page and closes the panel", async () => {
    panelStoreState.isOpen = true;

    renderWithShell(<AssistantPanel />);

    const fullPageButton = await screen.findByRole("button", { name: "Open full page" });
    await userEvent.click(fullPageButton);

    expect(mockSetOpen).toHaveBeenCalledWith(false);
    expect(mockPush).toHaveBeenCalledWith("/acme/assistant");
  });

  it("renders nothing when the instance reports the assistant unavailable", async () => {
    panelStoreState.isOpen = true;
    mockGetAvailability.mockResolvedValue({ enabled: false, model_label: "" });

    renderWithShell(<AssistantPanel />);

    await vi.waitFor(() => expect(mockGetAvailability).toHaveBeenCalled());
    expect(screen.queryByText("Assistant")).not.toBeInTheDocument();
  });
});

describe("AssistantPanel — active session", () => {
  it("renders the messages of the active session", async () => {
    panelStoreState.isOpen = true;
    assistantStoreState.activeSessionId = "s1";
    mockGetSessions.mockResolvedValue([
      {
        id: "s1",
        title: "Plan my week",
        focus_workspace_id: null,
        created_at: "2026-09-16T09:00:00Z",
        updated_at: "2026-09-16T09:00:00Z",
      },
    ]);
    mockGetMessages.mockResolvedValue([
      {
        id: "m1",
        session_id: "s1",
        role: "user",
        content: "What is on my plate?",
        created_at: "2026-09-16T09:00:00Z",
      },
      {
        id: "m2",
        session_id: "s1",
        role: "assistant",
        content: "Three issues are waiting on you.",
        created_at: "2026-09-16T09:00:01Z",
      },
    ]);

    renderWithShell(<AssistantPanel />);

    await screen.findByText("What is on my plate?");
    expect(screen.getByText("Three issues are waiting on you.")).toBeInTheDocument();
    expect(mockGetMessages).toHaveBeenCalledWith("s1");
  });

  it("sends a message on the active session when Enter is pressed", async () => {
    panelStoreState.isOpen = true;
    assistantStoreState.activeSessionId = "s1";
    mockGetMessages.mockResolvedValue([
      {
        id: "m1",
        session_id: "s1",
        role: "user",
        content: "hi",
        created_at: "2026-09-16T09:00:00Z",
      },
    ]);

    renderWithShell(<AssistantPanel />);

    const textarea = await screen.findByPlaceholderText("Message the Assistant...");
    await userEvent.type(textarea, "another question");
    await userEvent.keyboard("{Enter}");

    expect(mockSendMutate).toHaveBeenCalledWith(expect.objectContaining({
      content: "another question",
      request_id: expect.any(String),
      context: expect.objectContaining({ workspace_id: "ws-1" }),
    }));
  });
});

describe("AssistantFab — availability gate and placement", () => {
  it("does not render when the instance reports the assistant unavailable", async () => {
    mockGetAvailability.mockResolvedValue({ enabled: false, model_label: "" });

    renderWithShell(<AssistantFab />);

    await vi.waitFor(() => expect(mockGetAvailability).toHaveBeenCalled());
    expect(screen.queryByRole("button", { name: "Ask Agora" })).not.toBeInTheDocument();
  });

  it("renders when available and opens the panel on click", async () => {
    renderWithShell(<AssistantFab />);

    const fab = await screen.findByRole("button", { name: "Ask Agora" });
    await userEvent.click(fab);

    expect(mockToggle).toHaveBeenCalled();
  });

  it("hides while the panel is already open", async () => {
    panelStoreState.isOpen = true;

    renderWithShell(<AssistantFab />);

    await vi.waitFor(() => expect(mockGetAvailability).toHaveBeenCalled());
    expect(screen.queryByRole("button", { name: "Ask Agora" })).not.toBeInTheDocument();
  });

  it("hides on the full assistant page, where it would be redundant", async () => {
    renderWithShell(<AssistantFab />, "/acme/assistant");

    await vi.waitFor(() => expect(mockGetAvailability).toHaveBeenCalled());
    expect(screen.queryByRole("button", { name: "Ask Agora" })).not.toBeInTheDocument();
  });
});

// A reply that lands while nobody is looking leaves a dot; opening the panel
// (or standing on the full page) is what clears it.
describe("AssistantFab — unseen-result dot", () => {
  it("marks a finished run unseen when it succeeds", async () => {
    renderWithShell(<AssistantFab />);

    await screen.findByRole("button", { name: "Ask Agora" });
    act(() => {
      wsHandlers.get("assistant:run_finished")!({
        user_id: "u1",
        session_id: "s1",
        run_id: "r1",
        status: "ok",
      });
    });

    expect(mockMarkUnseenResult).toHaveBeenCalledTimes(1);
  });

  it("ignores a run that ended failed or cancelled — the transcript reports those", async () => {
    renderWithShell(<AssistantFab />);

    await screen.findByRole("button", { name: "Ask Agora" });
    act(() => {
      const notify = wsHandlers.get("assistant:run_finished")!;
      notify({ user_id: "u1", session_id: "s1", run_id: "r1", status: "failed" });
      notify({ user_id: "u1", session_id: "s1", run_id: "r2", status: "cancelled" });
      notify(null);
    });

    expect(mockMarkUnseenResult).not.toHaveBeenCalled();
  });

  it("labels the bubble differently while a result is waiting", async () => {
    panelStoreState.hasUnseenResult = true;

    renderWithShell(<AssistantFab />);

    expect(
      await screen.findByRole("button", { name: "Agora finished — open to read it" }),
    ).toBeInTheDocument();
  });

  it("clears the dot once the panel is open or the full page is showing", async () => {
    panelStoreState.isOpen = true;
    const openPanel = renderWithShell(<AssistantFab />);
    await vi.waitFor(() => expect(mockClearUnseenResult).toHaveBeenCalled());
    openPanel.unmount();

    mockClearUnseenResult.mockClear();
    panelStoreState.isOpen = false;
    renderWithShell(<AssistantFab />, "/acme/assistant");
    await vi.waitFor(() => expect(mockClearUnseenResult).toHaveBeenCalled());
  });
});

// Escape is the dismissal every other floating surface answers to. The one
// keystroke it must NOT swallow is the one the composer's slash menu ate.
describe("AssistantPanel — Escape", () => {
  it("closes the panel", async () => {
    panelStoreState.isOpen = true;

    renderWithShell(<AssistantPanel />);
    await screen.findByText("Assistant");
    await userEvent.keyboard("{Escape}");

    expect(mockSetOpen).toHaveBeenCalledWith(false);
  });

  it("leaves the panel open when the slash menu consumed the keystroke", async () => {
    panelStoreState.isOpen = true;
    assistantStoreState.activeSessionId = "s1";
    mockGetMessages.mockResolvedValue([
      { id: "m1", session_id: "s1", role: "user", content: "hi", created_at: "2026-09-16T09:00:00Z" },
    ]);

    renderWithShell(<AssistantPanel />);

    const textarea = await screen.findByPlaceholderText("Message the Assistant...");
    await userEvent.type(textarea, "/");
    expect(await screen.findByRole("listbox")).toBeInTheDocument();

    await userEvent.keyboard("{Escape}");

    expect(mockSetOpen).not.toHaveBeenCalled();
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });
});

// The pane doesn't fit a 380px popup: an artifact card here marks the
// artifact open on the session and hands off to the full page (plan §6).
describe("AssistantPanel — artifact hand-off", () => {
  it("closes the panel and navigates to the full page with the artifact open", async () => {
    panelStoreState.isOpen = true;
    assistantStoreState.activeSessionId = "session-1";
    mockGetSessions.mockResolvedValue([
      {
        id: "session-1",
        title: "Usage",
        focus_workspace_id: null,
        created_at: "2026-09-16T10:00:00Z",
        updated_at: "2026-09-16T10:00:00Z",
      },
    ]);
    mockGetMessages.mockResolvedValue([
      {
        id: "m1",
        session_id: "session-1",
        role: "tool",
        content: "{}",
        tool_name: "create_artifact",
        tool_result: {
          artifact_id: "art-1",
          title: "Agent usage by day",
          kind: "chart",
          version: 1,
        },
        created_at: "2026-09-16T10:00:00Z",
      },
    ]);

    renderWithShell(<AssistantPanel />);

    const card = await screen.findByRole("button", { name: /Agent usage by day/ });
    await userEvent.click(card);

    expect(mockSetOpenArtifact).toHaveBeenCalledWith("session-1", "art-1");
    expect(mockSetOpen).toHaveBeenCalledWith(false);
    expect(mockPush).toHaveBeenCalledWith("/acme/assistant");
  });
});

// The plan requires a failed send to hand the user their words back: the
// draft is written BEFORE the request and only cleared once the server
// accepted it (docs/agora-assistant-final-plan.md §2, "persisted drafts").
describe("AssistantPanel — draft preservation on a failed send", () => {
  beforeEach(() => {
    panelStoreState.isOpen = true;
    assistantStoreState.activeSessionId = "s1";
    mockGetMessages.mockResolvedValue([
      {
        id: "m1",
        session_id: "s1",
        role: "user",
        content: "hi",
        created_at: "2026-09-16T09:00:00Z",
      },
    ]);
  });

  it("leaves the typed message in the composer when the send fails", async () => {
    mockSendMutate.mockRejectedValue(new Error("network down"));

    renderWithShell(<AssistantPanel />);

    const textarea = await screen.findByPlaceholderText("Message the Assistant...");
    await userEvent.type(textarea, "close the login bug");
    await userEvent.keyboard("{Enter}");

    await vi.waitFor(() => expect(mockSendMutate).toHaveBeenCalled());
    await vi.waitFor(() =>
      expect(screen.getByPlaceholderText("Message the Assistant...")).toHaveValue(
        "close the login bug",
      ),
    );
    expect(vi.mocked(toast.error)).toHaveBeenCalledWith("Failed to send message");
    // Still in the store, so a reload or a surface switch keeps it too.
    expect(assistantStoreState.draftsBySession.s1?.content).toBe("close the login bug");
  });

  it("clears the composer once the send succeeds", async () => {
    mockSendMutate.mockResolvedValue({ message_id: "m2", run_id: "r1", created_at: "" });

    renderWithShell(<AssistantPanel />);

    const textarea = await screen.findByPlaceholderText("Message the Assistant...");
    await userEvent.type(textarea, "close the login bug");
    await userEvent.keyboard("{Enter}");

    await vi.waitFor(() =>
      expect(screen.getByPlaceholderText("Message the Assistant...")).toHaveValue(""),
    );
    expect(assistantStoreState.draftsBySession.s1).toBeUndefined();
  });
});

// The session's focus workspace is what tools default to, so it is shown
// beside the composer on every surface (plan §3, "Resolve context explicitly").
describe("AssistantPanel — context chip", () => {
  it("shows the session's focus workspace and clears it on removal", async () => {
    panelStoreState.isOpen = true;
    assistantStoreState.activeSessionId = "s1";
    mockGetSessions.mockResolvedValue([
      {
        id: "s1",
        title: "Plan my week",
        focus_workspace_id: "ws-1",
        created_at: "2026-09-16T09:00:00Z",
        updated_at: "2026-09-16T09:00:00Z",
      },
    ]);
    mockGetMessages.mockResolvedValue([
      { id: "m1", session_id: "s1", role: "user", content: "hi", created_at: "2026-09-16T09:00:00Z" },
    ]);

    renderWithShell(<AssistantPanel />);

    expect(await screen.findByText("acme")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Clear workspace focus" }));

    expect(mockUpdateMutate).toHaveBeenCalledWith(
      { sessionId: "s1", focus_workspace_id: null },
      expect.anything(),
    );
  });
});
