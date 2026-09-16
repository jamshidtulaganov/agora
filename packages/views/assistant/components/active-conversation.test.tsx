import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { act, render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import type { AssistantMessage } from "@agora/core/types";
import { NavigationProvider } from "../../navigation";
import type { NavigationAdapter } from "../../navigation";
import { RESOURCES } from "../../locales";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

vi.mock("@agora/core/paths", async () => {
  const actual = await vi.importActual<typeof import("@agora/core/paths")>("@agora/core/paths");
  return { ...actual, useCurrentWorkspace: () => ({ id: "ws-1", slug: "acme", name: "Acme" }) };
});

const mockGetMessages = vi.hoisted(() => vi.fn());
const mockGetRuns = vi.hoisted(() => vi.fn());
const mockSendMutate = vi.hoisted(() => vi.fn());
const mockCancelMutate = vi.hoisted(() => vi.fn());
const mockUpdateMutate = vi.hoisted(() => vi.fn());
const assistantStoreState = vi.hoisted(() => ({
  drafts: {} as Record<string, { content: string; request_id: string; context?: { workspace_id: string | null } }>,
}));

vi.mock("@agora/core/assistant", async () => {
  const { useSyncExternalStore } = await import("react");
  const listeners = new Set<() => void>();
  const setDraft = (
    sessionId: string,
    draft: { content: string; request_id: string } | null,
  ) => {
    assistantStoreState.drafts = { ...assistantStoreState.drafts };
    if (draft) assistantStoreState.drafts[sessionId] = draft;
    else delete assistantStoreState.drafts[sessionId];
    listeners.forEach((listener) => listener());
  };
  const state = () => ({ draftsBySession: assistantStoreState.drafts, setDraft });
  const useAssistantStore = Object.assign(
    (selector?: (s: ReturnType<typeof state>) => unknown) =>
      useSyncExternalStore(
        (listener) => {
          listeners.add(listener);
          return () => listeners.delete(listener);
        },
        () => (selector ? selector(state()) : state()),
      ),
    { getState: state },
  );

  return {
    useAssistantStore,
    assistantSessionListOptions: () => ({ queryKey: ["assistant", "sessions"], queryFn: () => [] }),
    assistantMessagesOptions: (sessionId: string) => ({
      queryKey: ["assistant", "messages", sessionId],
      queryFn: () => mockGetMessages(sessionId),
    }),
    assistantRunsOptions: (sessionId: string) => ({
      queryKey: ["assistant", "runs", sessionId],
      queryFn: () => mockGetRuns(sessionId),
    }),
    useSendAssistantMessage: () => ({ mutateAsync: mockSendMutate, isPending: false }),
    useCancelAssistantRun: () => ({ mutate: mockCancelMutate }),
    useUpdateAssistantSession: () => ({ mutate: mockUpdateMutate, isPending: false }),
  };
});

import { ActiveConversation } from "./active-conversation";

const MESSAGES_KEY = ["assistant", "messages", "s1"];

function message(overrides: Partial<AssistantMessage> & { id: string }): AssistantMessage {
  return {
    session_id: "s1",
    role: "user",
    content: "hello",
    created_at: "2026-09-16T09:00:00Z",
    ...overrides,
  } as AssistantMessage;
}

function renderConversation() {
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
  const view = render(
    <I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={qc}>
        <NavigationProvider value={nav}>
          <ActiveConversation
            sessionId="s1"
            initialMessage={null}
            onInitialMessageConsumed={() => {}}
          />
        </NavigationProvider>
      </QueryClientProvider>
    </I18nProvider>,
  );
  return { ...view, qc };
}

/** jsdom reports every box as 0×0 — give the transcript a real geometry so
 *  the stick-to-bottom arithmetic has something to decide on. */
function stubScroller(container: HTMLElement, scrollHeight = 4000, clientHeight = 500) {
  const el = container.querySelector<HTMLDivElement>(".overflow-y-auto");
  if (!el) throw new Error("transcript scroller not found");
  Object.defineProperty(el, "scrollHeight", { value: scrollHeight, configurable: true });
  Object.defineProperty(el, "clientHeight", { value: clientHeight, configurable: true });
  return el;
}

beforeEach(() => {
  vi.clearAllMocks();
  assistantStoreState.drafts = {};
  mockGetMessages.mockResolvedValue([
    message({ id: "m1", role: "user", content: "What is on my plate?" }),
    message({ id: "m2", role: "assistant", content: "Three issues are waiting on you." }),
  ]);
  mockGetRuns.mockResolvedValue([{ id: "run-1", status: "completed", active_tool: null, error: null }]);
  mockSendMutate.mockResolvedValue({ message_id: "m3", run_id: "run-2", created_at: "" });
});

afterEach(() => cleanup());

describe("ActiveConversation — stick to bottom", () => {
  it("follows new content while the reader is at the bottom", async () => {
    const { container, qc } = renderConversation();
    await screen.findByText("Three issues are waiting on you.");
    const scroller = stubScroller(container);
    scroller.scrollTop = 3500; // pinned to the bottom

    act(() => {
      qc.setQueryData<AssistantMessage[]>(MESSAGES_KEY, (old) => [
        ...(old ?? []),
        message({ id: "m3", role: "assistant", content: "And one more thing." }),
      ]);
    });

    await screen.findByText("And one more thing.");
    expect(scroller.scrollTop).toBe(4000);
    expect(screen.queryByRole("button", { name: "Jump to latest" })).not.toBeInTheDocument();
  });

  it("never yanks a reader who scrolled up — it offers the jump pill instead", async () => {
    const { container, qc } = renderConversation();
    await screen.findByText("Three issues are waiting on you.");
    const scroller = stubScroller(container);

    scroller.scrollTop = 200; // scrolled up to re-read
    fireEvent.scroll(scroller);
    // Nothing new yet: a detached reader alone must not summon the pill.
    expect(screen.queryByRole("button", { name: "Jump to latest" })).not.toBeInTheDocument();

    act(() => {
      qc.setQueryData<AssistantMessage[]>(MESSAGES_KEY, (old) => [
        ...(old ?? []),
        message({ id: "m3", role: "assistant", content: "And one more thing." }),
      ]);
    });

    const pill = await screen.findByRole("button", { name: "Jump to latest" });
    expect(scroller.scrollTop).toBe(200);

    await userEvent.click(pill);
    expect(scroller.scrollTop).toBe(4000);
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Jump to latest" })).not.toBeInTheDocument(),
    );
  });

  it("re-attaches on the reader's OWN send, even from halfway up the transcript", async () => {
    const { container, qc } = renderConversation();
    await screen.findByText("Three issues are waiting on you.");
    const scroller = stubScroller(container);

    scroller.scrollTop = 200;
    fireEvent.scroll(scroller);

    const textarea = screen.getByPlaceholderText("Message the Assistant...");
    await userEvent.type(textarea, "and the pricing page?");
    await userEvent.keyboard("{Enter}");
    await waitFor(() => expect(mockSendMutate).toHaveBeenCalled());

    act(() => {
      qc.setQueryData<AssistantMessage[]>(MESSAGES_KEY, (old) => [
        ...(old ?? []),
        message({ id: "m3", role: "user", content: "and the pricing page?" }),
      ]);
    });
    await screen.findByText("and the pricing page?");

    expect(scroller.scrollTop).toBe(4000);
    expect(screen.queryByRole("button", { name: "Jump to latest" })).not.toBeInTheDocument();
  });

  it("drops the pill again once the reader scrolls back down themselves", async () => {
    const { container, qc } = renderConversation();
    await screen.findByText("Three issues are waiting on you.");
    const scroller = stubScroller(container);

    scroller.scrollTop = 200;
    fireEvent.scroll(scroller);
    act(() => {
      qc.setQueryData<AssistantMessage[]>(MESSAGES_KEY, (old) => [
        ...(old ?? []),
        message({ id: "m3", role: "assistant", content: "And one more thing." }),
      ]);
    });
    await screen.findByRole("button", { name: "Jump to latest" });

    scroller.scrollTop = 3500;
    fireEvent.scroll(scroller);

    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Jump to latest" })).not.toBeInTheDocument(),
    );
  });
});

describe("ActiveConversation — failed run recovery", () => {
  beforeEach(() => {
    mockGetRuns.mockResolvedValue([
      { id: "run-1", status: "failed", active_tool: null, error: "Model provider timed out" },
    ]);
  });

  it("shows the user-safe error with a Retry that re-sends the last user message", async () => {
    renderConversation();

    const notice = await screen.findByRole("status");
    expect(notice).toHaveTextContent("The Assistant could not finish this reply");
    expect(notice).toHaveTextContent("Model provider timed out");

    await userEvent.click(screen.getByRole("button", { name: "Retry" }));

    await waitFor(() => expect(mockSendMutate).toHaveBeenCalledTimes(1));
    expect(mockSendMutate.mock.calls[0]![0]).toEqual(
      expect.objectContaining({
        content: "What is on my plate?",
        context: expect.objectContaining({ workspace_id: "ws-1" }),
      }),
    );
  });

  it("re-sends the SAME request id on a second Retry — one intent, never two turns", async () => {
    renderConversation();

    const retry = await screen.findByRole("button", { name: "Retry" });
    await userEvent.click(retry);
    await waitFor(() => expect(mockSendMutate).toHaveBeenCalledTimes(1));
    await userEvent.click(retry);
    await waitFor(() => expect(mockSendMutate).toHaveBeenCalledTimes(2));

    expect(mockSendMutate.mock.calls[1]![0].request_id).toBe(
      mockSendMutate.mock.calls[0]![0].request_id,
    );
  });

  it("hides Retry while a run is active — there is nothing to recover yet", async () => {
    mockGetRuns.mockResolvedValue([{ id: "run-1", status: "running", active_tool: null, error: null }]);

    renderConversation();

    await screen.findByText("Three issues are waiting on you.");
    expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
  });
});

describe("ActiveConversation — regenerate", () => {
  it("re-sends the previous user message from the last assistant row", async () => {
    renderConversation();

    await screen.findByText("Three issues are waiting on you.");
    await userEvent.click(screen.getByRole("button", { name: "Regenerate" }));

    await waitFor(() => expect(mockSendMutate).toHaveBeenCalledTimes(1));
    expect(mockSendMutate.mock.calls[0]![0].content).toBe("What is on my plate?");
  });

  it("offers regenerate on the tail only, not on older answers", async () => {
    mockGetMessages.mockResolvedValue([
      message({ id: "m1", role: "user", content: "first" }),
      message({ id: "m2", role: "assistant", content: "first answer" }),
      message({ id: "m3", role: "user", content: "second" }),
      message({ id: "m4", role: "assistant", content: "second answer" }),
    ]);

    renderConversation();

    await screen.findByText("second answer");
    expect(screen.getAllByRole("button", { name: "Regenerate" })).toHaveLength(1);
  });

  it("does not offer regenerate while a run is active", async () => {
    mockGetRuns.mockResolvedValue([{ id: "run-1", status: "running", active_tool: null, error: null }]);

    renderConversation();

    await screen.findByText("Three issues are waiting on you.");
    expect(screen.queryByRole("button", { name: "Regenerate" })).not.toBeInTheDocument();
  });
});

describe("ActiveConversation — follow-up chips", () => {
  beforeEach(() => {
    mockGetMessages.mockResolvedValue([
      message({ id: "m1", role: "user", content: "Create an issue for the pricing copy" }),
      message({
        id: "m2",
        role: "tool",
        content: "{}",
        tool_name: "create_issue",
        tool_result: { title: "Pricing copy" },
      }),
      message({ id: "m3", role: "assistant", content: "Created it." }),
    ]);
  });

  it("suggests follow-ups after a finished run and prefills (never sends) on click", async () => {
    renderConversation();

    const chip = await screen.findByRole("button", { name: "Assign it to someone" });
    expect(screen.getByRole("button", { name: "Set a due date" })).toBeInTheDocument();

    await userEvent.click(chip);

    expect(screen.getByPlaceholderText("Message the Assistant...")).toHaveValue(
      "Assign it to someone",
    );
    expect(mockSendMutate).not.toHaveBeenCalled();
    // Typing is intent — the suggestions get out of the way.
    expect(screen.queryByRole("button", { name: "Set a due date" })).not.toBeInTheDocument();
  });

  it("stays silent while the run is still going", async () => {
    mockGetRuns.mockResolvedValue([{ id: "run-1", status: "running", active_tool: null, error: null }]);

    renderConversation();

    await screen.findByText("Created it.");
    expect(screen.queryByRole("button", { name: "Assign it to someone" })).not.toBeInTheDocument();
  });
});
