import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const {
  authState,
  listMyInvitationsMock,
  listWorkspacesMock,
  routerBackMock,
  routerPushMock,
  routerReplaceMock,
} = vi.hoisted(() => ({
  authState: {
    user: null as null | { id: string; onboarded_at: string | null },
    isLoading: false,
  },
  listMyInvitationsMock: vi.fn(),
  listWorkspacesMock: vi.fn(),
  routerBackMock: vi.fn(),
  routerPushMock: vi.fn(),
  routerReplaceMock: vi.fn(),
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({
    back: routerBackMock,
    push: routerPushMock,
    replace: routerReplaceMock,
  }),
}));

vi.mock("@agora/core/auth", () => ({
  useAuthStore: (selector: (state: typeof authState) => unknown) =>
    selector(authState),
}));

vi.mock("@agora/core/api", () => ({
  api: {
    listMyInvitations: listMyInvitationsMock,
    listWorkspaces: listWorkspacesMock,
  },
}));

vi.mock("@agora/views/workspace/new-workspace-page", () => ({
  NewWorkspacePage: ({ onBack }: { onBack?: () => void }) => (
    <button type="button" onClick={onBack}>
      Back
    </button>
  ),
}));

import NewWorkspaceRoute from "./page";

function renderRoute() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <NewWorkspaceRoute />
    </QueryClientProvider>,
  );
}

describe("new workspace route", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    authState.user = { id: "user-1", onboarded_at: null };
    authState.isLoading = false;
    listWorkspacesMock.mockResolvedValue([]);
    listMyInvitationsMock.mockResolvedValue([]);
  });

  it("recovers a new user with one pending invitation to that invite", async () => {
    listMyInvitationsMock.mockResolvedValue([{ id: "invite-1" }]);

    renderRoute();

    await waitFor(() =>
      expect(routerReplaceMock).toHaveBeenCalledWith("/invite/invite-1"),
    );
    expect(screen.queryByRole("button", { name: "Back" })).not.toBeInTheDocument();
  });

  it("routes a new user without invitations to onboarding", async () => {
    renderRoute();

    await waitFor(() =>
      expect(routerReplaceMock).toHaveBeenCalledWith("/onboarding"),
    );
  });

  it("always gives an onboarded zero-workspace user a browser back action", async () => {
    authState.user = { id: "user-1", onboarded_at: "2026-08-04T00:00:00Z" };
    const user = userEvent.setup();

    renderRoute();
    await user.click(await screen.findByRole("button", { name: "Back" }));

    expect(routerBackMock).toHaveBeenCalledOnce();
  });

  it("returns an existing member to the root workspace resolver", async () => {
    authState.user = { id: "user-1", onboarded_at: "2026-08-04T00:00:00Z" };
    listWorkspacesMock.mockResolvedValue([{ id: "ws-1", slug: "acme" }]);
    const user = userEvent.setup();

    renderRoute();
    await user.click(await screen.findByRole("button", { name: "Back" }));

    expect(routerPushMock).toHaveBeenCalledWith("/");
  });
});
