import { type ReactNode } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const ApiError = vi.hoisted(() => {
  class ApiError extends Error {
    readonly status: number;
    constructor(message: string, status: number) {
      super(message);
      this.name = "ApiError";
      this.status = status;
    }
  }
  return ApiError;
});

type MutateOpts<T> = {
  onSuccess?: (data: T) => void;
  onError?: (err: unknown) => void;
};

const mockConnect = vi.hoisted(() =>
  vi.fn<(vars: undefined, opts?: MutateOpts<{ url: string }>) => void>(),
);
const mockDisconnect = vi.hoisted(() =>
  vi.fn<(vars: undefined, opts?: MutateOpts<void>) => void>(),
);
const mockRefetch = vi.hoisted(() => vi.fn());
const mockConnectWsId = vi.hoisted(() => vi.fn());
const mockDisconnectWsId = vi.hoisted(() => vi.fn());
const mockOpenExternal = vi.hoisted(() => vi.fn());

const queryRef = vi.hoisted(() => ({
  current: { data: undefined as unknown, isError: false },
}));

const queryOptsRef = vi.hoisted(() => ({
  current: undefined as { queryKey?: unknown; enabled?: boolean } | undefined,
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey?: unknown; enabled?: boolean }) => {
    queryOptsRef.current = opts;
    return { ...queryRef.current, refetch: mockRefetch };
  },
  queryOptions: <T,>(opts: T) => opts,
}));

vi.mock("@agora/core/api", () => ({ ApiError }));

vi.mock("@agora/core/zoho", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@agora/core/zoho")>();
  return {
    EMPTY_ZOHO_ACCOUNT: actual.EMPTY_ZOHO_ACCOUNT,
    zohoAccountState: actual.zohoAccountState,
    myZohoAccountOptions: (wsId: string) => ({
      queryKey: ["me", "zoho-account", wsId],
    }),
    useConnectZohoAccount: (wsId: string) => {
      mockConnectWsId(wsId);
      return { mutate: mockConnect, isPending: false };
    },
    useDisconnectZohoAccount: (wsId: string) => {
      mockDisconnectWsId(wsId);
      return { mutate: mockDisconnect, isPending: false };
    },
  };
});

vi.mock("../../platform", () => ({ openExternal: mockOpenExternal }));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), message: vi.fn() },
}));

import { ZohoAccountCard } from "./zoho-account-card";
import { toast } from "sonner";

const copy = enSettings.zoho.account;

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

function renderCard() {
  return render(<ZohoAccountCard wsId="ws-1" />, { wrapper: I18nWrapper });
}

const CONNECTED = {
  available: true,
  connected: true,
  status: "connected",
  email: "shohruh.a@octanefuel.com",
  name: "Shohruh A.",
  crm_role: "Collections Agent",
  crm_profile: "Standard",
  desk_departments: ["Collections"],
  checked_at: "2026-09-25T10:00:00Z",
};

const NOT_CONNECTED = {
  available: true,
  connected: false,
  status: "reconnect",
  email: "",
  name: "",
  crm_role: "",
  crm_profile: "",
  desk_departments: [],
  checked_at: null,
};

beforeEach(() => {
  vi.clearAllMocks();
  queryRef.current = { data: undefined, isError: false };
});

describe("ZohoAccountCard", () => {
  it("reads and connects the account through this workspace", () => {
    renderCard();
    expect(queryOptsRef.current?.queryKey).toEqual(["me", "zoho-account", "ws-1"]);
    expect(queryOptsRef.current?.enabled).toBe(true);
    expect(mockConnectWsId).toHaveBeenCalledWith("ws-1");
    expect(mockDisconnectWsId).toHaveBeenCalledWith("ws-1");
  });

  it("shows only the title while the account is loading", () => {
    renderCard();
    expect(screen.getByText(copy.title)).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
    expect(screen.queryByText(copy.not_available)).toBeNull();
    expect(screen.queryByText(copy.description)).toBeNull();
  });

  it("waits for the connector when the workspace has none", () => {
    queryRef.current = {
      data: { ...NOT_CONNECTED, available: false },
      isError: false,
    };
    renderCard();
    expect(screen.getByText(copy.not_available)).toBeTruthy();
    expect(screen.queryByText(copy.description)).toBeNull();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("treats a failed load as not available yet", () => {
    queryRef.current = { data: undefined, isError: true };
    renderCard();
    expect(screen.getByText(copy.not_available)).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("explains the connection and connects through the browser", async () => {
    queryRef.current = { data: NOT_CONNECTED, isError: false };
    const url = "https://accounts.zoho.com/oauth/v2/auth?client_id=x";
    mockConnect.mockImplementation((_vars, opts) => opts?.onSuccess?.({ url }));
    renderCard();

    expect(screen.getByText(copy.description)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: copy.connect }));

    expect(mockConnect).toHaveBeenCalledTimes(1);
    expect(mockOpenExternal).toHaveBeenCalledWith(url);
  });

  it("shows the server's message when connecting fails", async () => {
    queryRef.current = { data: NOT_CONNECTED, isError: false };
    mockConnect.mockImplementation((_vars, opts) =>
      opts?.onError?.(new ApiError("Zoho is busy, try again in a minute", 503)),
    );
    renderCard();

    await userEvent.click(screen.getByRole("button", { name: copy.connect }));

    expect(toast.error).toHaveBeenCalledWith("Zoho is busy, try again in a minute");
    expect(mockOpenExternal).not.toHaveBeenCalled();
  });

  it("falls back to a plain message when the sign-in link is missing", async () => {
    queryRef.current = { data: NOT_CONNECTED, isError: false };
    mockConnect.mockImplementation((_vars, opts) =>
      opts?.onError?.(new Error("Zoho connect response did not include a sign-in url")),
    );
    renderCard();

    await userEvent.click(screen.getByRole("button", { name: copy.connect }));

    expect(toast.error).toHaveBeenCalledWith(copy.error_connect_failed);
    expect(mockOpenExternal).not.toHaveBeenCalled();
  });

  it("shows who is connected and what Zoho lets them see", () => {
    queryRef.current = { data: CONNECTED, isError: false };
    renderCard();

    expect(screen.getByText(copy.description)).toBeTruthy();
    expect(screen.getByText("Connected as shohruh.a@octanefuel.com")).toBeTruthy();
    expect(
      screen.getByText("CRM: Collections Agent (Standard) · Desk: Collections"),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: copy.connect })).toBeNull();
  });

  it("builds the details line only from the parts that are present", () => {
    queryRef.current = {
      data: { ...CONNECTED, crm_profile: "", desk_departments: [] },
      isError: false,
    };
    const { unmount } = renderCard();
    expect(screen.getByText("CRM: Collections Agent")).toBeTruthy();
    unmount();

    queryRef.current = {
      data: {
        ...CONNECTED,
        crm_role: "",
        crm_profile: "",
        desk_departments: ["Collections", "Billing"],
      },
      isError: false,
    };
    renderCard();
    expect(screen.getByText("Desk: Collections, Billing")).toBeTruthy();
    expect(screen.queryByText(/CRM:/)).toBeNull();
  });

  it("asks before disconnecting, then disconnects", async () => {
    queryRef.current = { data: CONNECTED, isError: false };
    renderCard();

    await userEvent.click(screen.getByRole("button", { name: copy.disconnect }));
    expect(mockDisconnect).not.toHaveBeenCalled();
    expect(await screen.findByText(copy.disconnect_confirm_title)).toBeTruthy();

    const confirm = screen
      .getAllByRole("button", { name: copy.disconnect })
      .find((el) => el.getAttribute("data-slot") === "alert-dialog-action");
    expect(confirm).toBeTruthy();
    await userEvent.click(confirm!);

    expect(mockDisconnect).toHaveBeenCalledTimes(1);
  });

  it("does not disconnect when the confirmation is cancelled", async () => {
    queryRef.current = { data: CONNECTED, isError: false };
    renderCard();

    await userEvent.click(screen.getByRole("button", { name: copy.disconnect }));
    await userEvent.click(await screen.findByRole("button", { name: copy.cancel }));

    await waitFor(() =>
      expect(screen.queryByText(copy.disconnect_confirm_title)).toBeNull(),
    );
    expect(mockDisconnect).not.toHaveBeenCalled();
  });

  it("warns and offers Reconnect when Zoho stopped accepting the connection", async () => {
    queryRef.current = {
      data: { ...CONNECTED, status: "reconnect" },
      isError: false,
    };
    const url = "https://accounts.zoho.com/oauth/v2/auth?client_id=y";
    mockConnect.mockImplementation((_vars, opts) => opts?.onSuccess?.({ url }));
    renderCard();

    expect(screen.getByText(copy.reconnect_warning)).toBeTruthy();
    expect(screen.queryByText(/Connected as/)).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: copy.reconnect }));

    expect(mockOpenExternal).toHaveBeenCalledWith(url);
    // The broken connection can still be removed.
    expect(screen.getByRole("button", { name: copy.disconnect })).toBeTruthy();
  });

  it("still shows an account connected through another workspace", () => {
    queryRef.current = {
      data: { ...CONNECTED, available: false },
      isError: false,
    };
    renderCard();

    expect(screen.getByText("Connected as shohruh.a@octanefuel.com")).toBeTruthy();
    expect(screen.queryByText(copy.not_available)).toBeNull();
    expect(screen.getByRole("button", { name: copy.disconnect })).toBeTruthy();
  });

  it("does not offer Reconnect without this workspace's connector", () => {
    queryRef.current = {
      data: { ...CONNECTED, available: false, status: "reconnect" },
      isError: false,
    };
    renderCard();

    expect(screen.getByText(copy.reconnect_warning)).toBeTruthy();
    expect(screen.queryByRole("button", { name: copy.reconnect })).toBeNull();
    expect(screen.getByRole("button", { name: copy.disconnect })).toBeTruthy();
  });

  it("says the connector is missing and refreshes on a 409", async () => {
    queryRef.current = { data: NOT_CONNECTED, isError: false };
    mockConnect.mockImplementation((_vars, opts) =>
      opts?.onError?.(
        new ApiError("zoho connector is not set up for this workspace", 409),
      ),
    );
    renderCard();

    await userEvent.click(screen.getByRole("button", { name: copy.connect }));

    expect(toast.error).toHaveBeenCalledWith(copy.not_available);
    expect(mockRefetch).toHaveBeenCalledWith({ cancelRefetch: false });
    expect(mockOpenExternal).not.toHaveBeenCalled();
  });

  it("refetches when the window regains focus", () => {
    queryRef.current = { data: NOT_CONNECTED, isError: false };
    renderCard();
    fireEvent.focus(window);
    expect(mockRefetch).toHaveBeenCalledWith({ cancelRefetch: false });
  });
});
