import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import { assistantKeys } from "@agora/core/assistant";
import { workspaceKeys } from "@agora/core/workspace";
import type { AssistantSession, Workspace } from "@agora/core/types";
import { RESOURCES } from "../../locales";

const toastError = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { error: toastError, success: vi.fn() } }));

const mockUpdate = vi.hoisted(() => vi.fn());
vi.mock("@agora/core/assistant", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@agora/core/assistant")>()),
  useUpdateAssistantSession: () => ({ mutate: mockUpdate, isPending: false }),
}));

import { AssistantContextChip } from "./context-chip";

function session(overrides: Partial<AssistantSession> = {}): AssistantSession {
  return {
    id: "session-1",
    title: "Plan my week",
    focus_workspace_id: "ws-1",
    created_at: "2026-09-17T09:00:00Z",
    updated_at: "2026-09-17T09:00:00Z",
    ...overrides,
  };
}

/** The chip reads ONLY from the cache (enabled: false) — seed it like the
 *  surrounding page/panel already would. */
function renderChip(sessions: AssistantSession[], workspaces: Partial<Workspace>[]) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  qc.setQueryData(assistantKeys.sessions(), sessions);
  qc.setQueryData(workspaceKeys.list(), workspaces);
  return render(
    <I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={qc}>
        <AssistantContextChip sessionId="session-1" />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

describe("AssistantContextChip", () => {
  it("shows the session's focus workspace by slug", () => {
    renderChip([session()], [{ id: "ws-1", slug: "acme", name: "Acme" }]);

    expect(screen.getByText("acme")).toBeInTheDocument();
  });

  it("shows the SESSION's focus even when the user is looking at another workspace", () => {
    // The panel opens over /other/issues; tools still default to the session
    // focus, so that is what the chip must say.
    renderChip(
      [session({ focus_workspace_id: "ws-2" })],
      [
        { id: "ws-1", slug: "acme", name: "Acme" },
        { id: "ws-2", slug: "globex", name: "Globex" },
      ],
    );

    expect(screen.getByText("globex")).toBeInTheDocument();
    expect(screen.queryByText("acme")).not.toBeInTheDocument();
  });

  it("renders nothing for a session with no focus workspace", () => {
    renderChip([session({ focus_workspace_id: null })], [{ id: "ws-1", slug: "acme" }]);

    expect(screen.queryByRole("button", { name: "Clear workspace focus" })).not.toBeInTheDocument();
  });

  it("still renders when the workspace id cannot be resolved to a slug", () => {
    // A workspace the user just lost access to: "scoped somewhere" beats
    // silently showing nothing.
    renderChip([session({ focus_workspace_id: "ws-gone" })], [{ id: "ws-1", slug: "acme" }]);

    expect(screen.getByText("Focused workspace")).toBeInTheDocument();
  });

  it("clears the focus to null when removed", async () => {
    renderChip([session()], [{ id: "ws-1", slug: "acme", name: "Acme" }]);

    await userEvent.click(screen.getByRole("button", { name: "Clear workspace focus" }));

    expect(mockUpdate).toHaveBeenCalledWith(
      { sessionId: "session-1", focus_workspace_id: null },
      expect.anything(),
    );
  });

  it("toasts when clearing the focus fails", async () => {
    mockUpdate.mockImplementation((_vars, opts?: { onError?: () => void }) => opts?.onError?.());
    renderChip([session()], [{ id: "ws-1", slug: "acme", name: "Acme" }]);

    await userEvent.click(screen.getByRole("button", { name: "Clear workspace focus" }));

    expect(toastError).toHaveBeenCalledWith("Couldn't clear the workspace focus");
  });
});
