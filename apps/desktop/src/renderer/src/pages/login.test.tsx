import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useWindowOverlayStore } from "@/stores/window-overlay-store";

const { getInvitationAuthInfoMock } = vi.hoisted(() => ({
  getInvitationAuthInfoMock: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({
  api: { getInvitationAuthInfo: getInvitationAuthInfoMock },
}));

interface CapturedLoginProps {
  mode?: "login" | "signup";
  registrationContext?: "company" | "invitation";
  initialEmail?: string;
  emailLocked?: boolean;
  extra?: ReactNode;
}

const loginPageSpy = vi.fn<(props: CapturedLoginProps) => void>();

vi.mock("@agora/views/auth", () => ({
  LoginPage: (props: CapturedLoginProps) => {
    loginPageSpy(props);
    return (
      <div data-testid="login-page" data-mode={props.mode ?? "login"}>
        {props.extra}
      </div>
    );
  },
}));

vi.mock("@agora/views/i18n", () => ({
  useT: () => ({ t: () => "auth label" }),
}));

vi.mock("@agora/views/platform", () => ({ DragStrip: () => null }));
vi.mock("@agora/ui/components/common/agora-icon", () => ({
  AgoraIcon: () => null,
}));

import { DesktopLoginPage } from "./login";

function renderLogin() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <DesktopLoginPage />
    </QueryClientProvider>,
  );
}

describe("DesktopLoginPage", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    getInvitationAuthInfoMock.mockReset();
    loginPageSpy.mockClear();
    useWindowOverlayStore.setState({
      overlay: null,
      pendingInvitationId: null,
    });
    Object.defineProperty(window, "desktopAPI", {
      configurable: true,
      value: {
        runtimeConfig: {
          ok: true,
          config: {
            apiUrl: "https://api.example.com",
            appUrl: "https://app.example.com",
            wsUrl: "wss://api.example.com/ws",
          },
        },
      },
    });
  });

  it("lets a standalone desktop user switch from login to company signup", async () => {
    const user = userEvent.setup();
    renderLogin();

    expect(screen.getByTestId("login-page")).toHaveAttribute("data-mode", "login");
    await user.click(screen.getByRole("button"));

    expect(screen.getByTestId("login-page")).toHaveAttribute("data-mode", "signup");
    expect(loginPageSpy.mock.calls.at(-1)?.[0]).toMatchObject({
      mode: "signup",
      registrationContext: "company",
      emailLocked: false,
    });
  });

  it("opens a new invitee on registration with the invitation email locked", async () => {
    getInvitationAuthInfoMock.mockResolvedValue({
      invitee_email: "invited@example.com",
      account_exists: false,
    });
    act(() => {
      useWindowOverlayStore.getState().setPendingInvitation("invite-1");
    });

    renderLogin();

    await waitFor(() =>
      expect(loginPageSpy.mock.calls.at(-1)?.[0]).toMatchObject({
        mode: "signup",
        registrationContext: "invitation",
        initialEmail: "invited@example.com",
        emailLocked: true,
      }),
    );
  });

  it("keeps an existing invited account on login", async () => {
    getInvitationAuthInfoMock.mockResolvedValue({
      invitee_email: "member@example.com",
      account_exists: true,
    });
    act(() => {
      useWindowOverlayStore.getState().setPendingInvitation("invite-2");
    });

    renderLogin();

    await waitFor(() =>
      expect(loginPageSpy.mock.calls.at(-1)?.[0]).toMatchObject({
        mode: "login",
        registrationContext: "invitation",
        initialEmail: "member@example.com",
        emailLocked: true,
      }),
    );
  });
});
