import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { AssistantSession } from "@agora/core/types";
import { ApiError } from "@agora/core/api";

const state = vi.hoisted(() => ({ activeSessionId: "old-session" as string | null }));
const setActiveSession = vi.hoisted(() => vi.fn());
const getSession = vi.hoisted(() => vi.fn());

vi.mock("@agora/core/assistant", () => ({
  useAssistantStore: (selector: (value: { activeSessionId: string | null; setActiveSession: typeof setActiveSession }) => unknown) =>
    selector({ activeSessionId: state.activeSessionId, setActiveSession }),
  assistantSessionOptions: (id: string) => ({
    queryKey: ["assistant", "session", id],
    queryFn: () => getSession(id),
    enabled: !!id,
    staleTime: Infinity,
  }),
}));

import { useHealActiveAssistantSession } from "./use-active-session";

const recent: AssistantSession[] = [{
  id: "recent-session", title: "Recent", focus_workspace_id: null,
  created_at: "2026-09-17", updated_at: "2026-09-17",
}];

function renderHeal(sessions: AssistantSession[] = recent) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const result = renderHook(() => useHealActiveAssistantSession(sessions, true), {
    wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
  });
  return { ...result, client };
}

beforeEach(() => {
  vi.clearAllMocks();
  state.activeSessionId = "old-session";
});
afterEach(cleanup);

describe("useHealActiveAssistantSession", () => {
  it("keeps an older active session omitted from the capped list when detail exists", async () => {
    getSession.mockResolvedValue({ id: "old-session" });
    const { client } = renderHeal();
    await waitFor(() => expect(client.getQueryState(["assistant", "session", "old-session"])?.status).toBe("success"));
    expect(setActiveSession).not.toHaveBeenCalled();
  });

  it("heals a deleted active session after its detail request fails", async () => {
    getSession.mockRejectedValue(new ApiError("not found", 404, "Not Found"));
    renderHeal();
    await waitFor(() => expect(setActiveSession).toHaveBeenCalledWith("recent-session"));
  });

  it("keeps the older session on a transient detail failure", async () => {
    getSession.mockRejectedValue(new ApiError("temporarily unavailable", 500, "Server Error"));
    const { client } = renderHeal();
    await waitFor(() => expect(client.getQueryState(["assistant", "session", "old-session"])?.status).toBe("error"));
    expect(setActiveSession).not.toHaveBeenCalled();
  });

  it("heals a malformed successful detail response", async () => {
    getSession.mockResolvedValue({ id: "" });
    renderHeal();
    await waitFor(() => expect(setActiveSession).toHaveBeenCalledWith("recent-session"));
  });

  it("selects the newest recent session when none was active", async () => {
    state.activeSessionId = null;
    renderHeal();
    await waitFor(() => expect(setActiveSession).toHaveBeenCalledWith("recent-session"));
    expect(getSession).not.toHaveBeenCalled();
  });
});
