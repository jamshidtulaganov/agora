import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "@agora/views/locales/en/common.json";
import enAuth from "@agora/views/locales/en/auth.json";
import enSettings from "@agora/views/locales/en/settings.json";
import type { ReactNode } from "react";

const TEST_RESOURCES = {
  en: { common: enCommon, auth: enAuth, settings: enSettings },
};

function createWrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    </I18nProvider>
  );
}

const {
  mockSendCode,
  mockVerifyCode,
  mockIssueCliToken,
  mockGetInvitationAuthInfo,
  mockRouterReplace,
  searchParamsState,
  authStateRef,
} = vi.hoisted(() => ({
  mockSendCode: vi.fn(),
  mockVerifyCode: vi.fn(),
  mockIssueCliToken: vi.fn(),
  mockGetInvitationAuthInfo: vi.fn(),
  mockRouterReplace: vi.fn(),
  searchParamsState: { params: new URLSearchParams() },
  authStateRef: {
    state: {
      sendCode: vi.fn(),
      verifyCode: vi.fn(),
      user: null as null | { id: string; email: string },
      isLoading: false,
    },
  },
}));

// Mock next/navigation
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: mockRouterReplace }),
  usePathname: () => "/login",
  useSearchParams: () => searchParamsState.params,
}));

// Mock auth store — shared LoginPage uses getState().sendCode/verifyCode,
// web wrapper uses useAuthStore((s) => s.user/isLoading). Keep the real
// sanitizeNextUrl so the redirect-sanitization rules are exercised rather
// than silently drifting behind a mock reimplementation.
vi.mock("@agora/core/auth", async () => {
  const actual =
    await vi.importActual<typeof import("@agora/core/auth")>(
      "@agora/core/auth",
    );
  authStateRef.state.sendCode = mockSendCode;
  authStateRef.state.verifyCode = mockVerifyCode;
  const useAuthStore = Object.assign(
    (selector: (s: typeof authStateRef.state) => unknown) =>
      selector(authStateRef.state),
    { getState: () => authStateRef.state },
  );
  return { ...actual, useAuthStore };
});

// Mock auth-cookie
vi.mock("@/features/auth/auth-cookie", () => ({
  setLoggedInCookie: vi.fn(),
}));

// Mock api
vi.mock("@agora/core/api", () => ({
  api: {
    listWorkspaces: vi.fn().mockResolvedValue([]),
    verifyCode: vi.fn(),
    setToken: vi.fn(),
    getMe: vi.fn(),
    issueCliToken: mockIssueCliToken,
    getInvitationAuthInfo: mockGetInvitationAuthInfo,
  },
}));

import LoginPage from "./page";

describe("LoginPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    searchParamsState.params = new URLSearchParams();
    authStateRef.state.user = null;
    authStateRef.state.isLoading = false;
  });

  it("renders login form with email input and continue button", () => {
    render(<LoginPage />, { wrapper: createWrapper() });

    expect(screen.getByText("Sign in to Agora")).toBeInTheDocument();
    expect(screen.getByText("Enter your email to get a login code")).toBeInTheDocument();
    expect(screen.getByLabelText("Email")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Continue" })
    ).toBeInTheDocument();
  });

  it("does not call sendCode when email is empty", async () => {
    const user = userEvent.setup();
    render(<LoginPage />, { wrapper: createWrapper() });

    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(mockSendCode).not.toHaveBeenCalled();
  });

  it("calls sendCode with email on submit", async () => {
    mockSendCode.mockResolvedValueOnce(undefined);
    const user = userEvent.setup();
    render(<LoginPage />, { wrapper: createWrapper() });

    await user.type(screen.getByLabelText("Email"), "test@agora.dev");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    await waitFor(() => {
      expect(mockSendCode).toHaveBeenCalledWith("test@agora.dev", "login");
    });
  });

  it("shows 'Sending code...' while submitting", async () => {
    mockSendCode.mockReturnValueOnce(new Promise(() => {}));
    const user = userEvent.setup();
    render(<LoginPage />, { wrapper: createWrapper() });

    await user.type(screen.getByLabelText("Email"), "test@agora.dev");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    await waitFor(() => {
      expect(screen.getByText("Sending code...")).toBeInTheDocument();
    });
  });

  it("shows verification code step after sending code", async () => {
    mockSendCode.mockResolvedValueOnce(undefined);
    const user = userEvent.setup();
    render(<LoginPage />, { wrapper: createWrapper() });

    await user.type(screen.getByLabelText("Email"), "test@agora.dev");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    await waitFor(() => {
      expect(screen.getByText("Check your email")).toBeInTheDocument();
    });
  });

  it("shows error when sendCode fails", async () => {
    mockSendCode.mockRejectedValueOnce(new Error("Network error"));
    const user = userEvent.setup();
    render(<LoginPage />, { wrapper: createWrapper() });

    await user.type(screen.getByLabelText("Email"), "test@agora.dev");
    await user.click(screen.getByRole("button", { name: "Continue" }));

    await waitFor(() => {
      expect(screen.getByText("Network error")).toBeInTheDocument();
    });
  });

  it("redirects a brand-new invitee from login to invitation signup", async () => {
    searchParamsState.params = new URLSearchParams({
      next: "/invite/a7a369b1-e7fc-4388-a519-0f1eaa9373ec",
    });
    mockGetInvitationAuthInfo.mockResolvedValue({
      invitee_email: "invited@example.com",
      account_exists: false,
    });

    render(<LoginPage />, { wrapper: createWrapper() });

    await waitFor(() =>
      expect(mockRouterReplace).toHaveBeenCalledWith(
        "/signup?next=%2Finvite%2Fa7a369b1-e7fc-4388-a519-0f1eaa9373ec",
      ),
    );
  });

  it("prefills and locks the invitation email for an existing account", async () => {
    searchParamsState.params = new URLSearchParams({
      next: "/invite/a7a369b1-e7fc-4388-a519-0f1eaa9373ec",
    });
    mockGetInvitationAuthInfo.mockResolvedValue({
      invitee_email: "Member@Example.com",
      account_exists: true,
    });

    render(<LoginPage />, { wrapper: createWrapper() });

    const email = await screen.findByLabelText("Email");
    expect(email).toHaveValue("member@example.com");
    expect(email).toBeDisabled();
    expect(mockRouterReplace).not.toHaveBeenCalled();
  });

  // Regression: MUL-1080 — if the user is already authenticated on the web
  // and the Desktop app redirects them to /login?platform=desktop, the web
  // must exchange the cookie session for a bearer token and hand it off via
  // the agora:// deep link, not silently redirect to the workspace page.
  it("mints a token and deep-links to Desktop when already logged in with platform=desktop", async () => {
    searchParamsState.params = new URLSearchParams({ platform: "desktop" });
    authStateRef.state.user = { id: "u1", email: "test@agora.dev" };
    mockIssueCliToken.mockImplementation(() =>
      Promise.resolve({ token: "handoff-jwt" }),
    );

    const hrefSetter = vi.fn();
    const originalLocation = window.location;
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { ...originalLocation, set href(value: string) { hrefSetter(value); } },
    });

    try {
      render(<LoginPage />, { wrapper: createWrapper() });

      await waitFor(() => {
        expect(mockIssueCliToken).toHaveBeenCalledTimes(1);
      });
      await waitFor(() => {
        expect(hrefSetter).toHaveBeenCalledWith(
          "agora://auth/callback?token=handoff-jwt",
        );
      });
      expect(
        await screen.findByRole("button", { name: "Open Agora Desktop" }),
      ).toBeInTheDocument();
    } finally {
      Object.defineProperty(window, "location", {
        configurable: true,
        value: originalLocation,
      });
    }
  });
});
