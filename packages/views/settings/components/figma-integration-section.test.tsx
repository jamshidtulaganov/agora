import { type ReactNode } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const ApiError = vi.hoisted(() => {
  class ApiError extends Error {
    readonly status: number;
    readonly statusText: string;
    readonly body?: unknown;
    constructor(message: string, status: number, statusText = "", body?: unknown) {
      super(message);
      this.name = "ApiError";
      this.status = status;
      this.statusText = statusText;
      this.body = body;
    }
  }
  return ApiError;
});

const mockGetStatus = vi.hoisted(() => vi.fn());
const mockPut = vi.hoisted(() => vi.fn());
const mockDelete = vi.hoisted(() => vi.fn());
const mockInvalidate = vi.hoisted(() => vi.fn());

type MemberRole = "owner" | "admin" | "member";

const membersRef = vi.hoisted(() => ({
  current: [{ user_id: "user-1", role: "owner" as MemberRole }],
}));
const statusRef = vi.hoisted(() => ({
  current: { configured: false } as Record<string, unknown> | undefined,
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[]; enabled?: boolean }) => {
    if (opts.enabled === false) return { data: undefined, isLoading: false };
    const key = JSON.stringify(opts.queryKey);
    if (key.includes("members")) return { data: membersRef.current, isLoading: false };
    if (key.includes("figma-credential")) return { data: statusRef.current, isLoading: false };
    return { data: undefined, isLoading: false };
  },
  useQueryClient: () => ({ invalidateQueries: mockInvalidate }),
  queryOptions: <T,>(opts: T) => opts,
}));

vi.mock("@agora/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@agora/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"], queryFn: vi.fn() }),
}));

vi.mock("@agora/core/api", () => ({
  api: {
    getFigmaCredentialStatus: mockGetStatus,
    putFigmaCredential: mockPut,
    deleteFigmaCredential: mockDelete,
  },
  ApiError,
}));

vi.mock("@agora/core/auth", () => {
  const useAuthStore = Object.assign(
    (sel?: (s: { user: { id: string } }) => unknown) =>
      sel ? sel({ user: { id: "user-1" } }) : { user: { id: "user-1" } },
    { getState: () => ({ user: { id: "user-1" } }) },
  );
  return { useAuthStore };
});

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), message: vi.fn() },
}));

import { FigmaIntegrationSection } from "./figma-integration-section";
import { toast } from "sonner";

const TEST_RESOURCES = {
  en: { common: enCommon, settings: enSettings },
};

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function renderSection() {
  return render(<FigmaIntegrationSection />, { wrapper: I18nWrapper });
}

beforeEach(() => {
  vi.clearAllMocks();
  membersRef.current = [{ user_id: "user-1", role: "owner" }];
  statusRef.current = { configured: false };
});

describe("FigmaIntegrationSection", () => {
  it("renders the empty state with a save form for admins", () => {
    renderSection();
    expect(screen.getByText(enSettings.figma.not_configured)).toBeTruthy();
    expect(screen.getByLabelText(enSettings.figma.token_label)).toBeTruthy();
    expect(screen.getByText(enSettings.figma.save)).toBeTruthy();
  });

  it("hides the save form and remove button for plain members", () => {
    membersRef.current = [{ user_id: "user-1", role: "member" }];
    statusRef.current = {
      configured: true,
      label: "SD design",
      token_last4: "ab12",
      probe_status: "ok",
    };
    renderSection();
    expect(screen.getByText("SD design")).toBeTruthy();
    expect(screen.queryByLabelText(enSettings.figma.token_label)).toBeNull();
    expect(screen.queryByLabelText(enSettings.figma.remove)).toBeNull();
  });

  it("shows configured status with badges", () => {
    statusRef.current = {
      configured: true,
      label: "SD design",
      token_last4: "ab12",
      expires_at: "2026-09-30T00:00:00Z",
      expiring_soon: true,
      seat_probe: "low_seat",
      probe_status: "ok",
    };
    renderSection();
    expect(screen.getByText("SD design")).toBeTruthy();
    expect(screen.getByText("…ab12")).toBeTruthy();
    expect(screen.getByText(enSettings.figma.expiring_soon)).toBeTruthy();
    expect(screen.getByText(enSettings.figma.seat_warning)).toBeTruthy();
    expect(screen.getByText(enSettings.figma.status_ok)).toBeTruthy();
  });

  it("saves a token and refreshes the status", async () => {
    mockPut.mockResolvedValue({ configured: true });
    renderSection();
    await userEvent.type(screen.getByLabelText(enSettings.figma.token_label), "figd_secret");
    await userEvent.click(screen.getByText(enSettings.figma.save));
    await waitFor(() => expect(mockPut).toHaveBeenCalled());
    const [wsId, body] = mockPut.mock.calls[0]!;
    expect(wsId).toBe("workspace-1");
    expect(body.token).toBe("figd_secret");
    expect(body.expires_at).toBeTruthy(); // +90d prefill rides along
    expect(mockInvalidate).toHaveBeenCalled();
    expect(toast.success).toHaveBeenCalled();
  });

  it("surfaces a 422 as the invalid-token error", async () => {
    mockPut.mockRejectedValue(new ApiError("unprocessable", 422));
    renderSection();
    await userEvent.type(screen.getByLabelText(enSettings.figma.token_label), "bad");
    await userEvent.click(screen.getByText(enSettings.figma.save));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(enSettings.figma.error_invalid_token),
    );
  });

  it("removes the credential", async () => {
    statusRef.current = { configured: true, label: "SD design", token_last4: "ab12" };
    mockDelete.mockResolvedValue(undefined);
    renderSection();
    await userEvent.click(screen.getByLabelText(enSettings.figma.remove));
    await waitFor(() => expect(mockDelete).toHaveBeenCalledWith("workspace-1"));
    expect(mockInvalidate).toHaveBeenCalled();
  });
});
