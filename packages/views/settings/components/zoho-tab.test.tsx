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

const mockSaveConnection = vi.hoisted(() => vi.fn());
const mockDeleteConnection = vi.hoisted(() => vi.fn());
const mockBind = vi.hoisted(() => vi.fn());
const mockUnbind = vi.hoisted(() => vi.fn());
const mockPush = vi.hoisted(() => vi.fn());

type MemberRole = "owner" | "admin" | "member";

const membersRef = vi.hoisted(() => ({
  current: [{ user_id: "user-1", role: "owner" as MemberRole }],
}));
const connectionRef = vi.hoisted(() => ({
  current: { configured: false } as Record<string, unknown> | undefined,
}));
const bindingRef = vi.hoisted(() => ({
  current: { bound: false } as Record<string, unknown> | undefined,
}));
const configsRef = vi.hoisted(() => ({
  current: [] as Record<string, unknown>[],
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[]; enabled?: boolean }) => {
    if (opts.enabled === false) return { data: undefined, isLoading: false };
    const key = JSON.stringify(opts.queryKey);
    if (key.includes("members")) return { data: membersRef.current, isLoading: false };
    if (key.includes("zoho-connection")) return { data: connectionRef.current, isLoading: false };
    if (key.includes("zoho-user-binding")) return { data: bindingRef.current, isLoading: false };
    if (key.includes("zoho-sync-configs")) return { data: configsRef.current, isLoading: false };
    return { data: undefined, isLoading: false };
  },
  queryOptions: <T,>(opts: T) => opts,
}));

vi.mock("@agora/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@agora/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"], queryFn: vi.fn() }),
}));

vi.mock("@agora/core/api", () => ({ ApiError }));

vi.mock("@agora/core/auth", () => {
  const useAuthStore = Object.assign(
    (sel?: (s: { user: { id: string } }) => unknown) =>
      sel ? sel({ user: { id: "user-1" } }) : { user: { id: "user-1" } },
    { getState: () => ({ user: { id: "user-1" } }) },
  );
  return { useAuthStore };
});

vi.mock("@agora/core/paths", () => ({
  useWorkspacePaths: () => ({ zoho: () => "/acme/zoho" }),
}));

vi.mock("../../navigation", () => ({
  useNavigation: () => ({ push: mockPush }),
}));

vi.mock("@agora/core/zoho", () => ({
  ZOHO_DCS: ["us", "eu", "in", "au", "jp", "sa", "ca"],
  zohoConnectionOptions: (wsId: string) => ({
    queryKey: ["zoho-connection", wsId],
    queryFn: vi.fn(),
  }),
  zohoUserBindingOptions: (wsId: string) => ({
    queryKey: ["zoho-user-binding", wsId],
    queryFn: vi.fn(),
  }),
  zohoSyncConfigsOptions: (wsId: string) => ({
    queryKey: ["zoho-sync-configs", wsId],
    queryFn: vi.fn(),
  }),
  useSaveZohoConnection: () => ({ mutateAsync: mockSaveConnection, isPending: false }),
  useDeleteZohoConnection: () => ({ mutateAsync: mockDeleteConnection, isPending: false }),
  useSaveZohoUserBinding: () => ({ mutateAsync: mockBind, isPending: false }),
  useDeleteZohoUserBinding: () => ({ mutateAsync: mockUnbind, isPending: false }),
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), message: vi.fn() },
}));

import { ZohoTab } from "./zoho-tab";
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

function renderTab() {
  return render(<ZohoTab />, { wrapper: I18nWrapper });
}

beforeEach(() => {
  vi.clearAllMocks();
  membersRef.current = [{ user_id: "user-1", role: "owner" }];
  connectionRef.current = { configured: false };
  bindingRef.current = { bound: false };
  configsRef.current = [];
});

describe("ZohoTab — connection section", () => {
  it("shows the empty state with the credential form for owners", () => {
    renderTab();
    expect(screen.getByText(enSettings.zoho.connection.not_configured)).toBeTruthy();
    expect(screen.getByLabelText(enSettings.zoho.connection.client_id_label)).toBeTruthy();
    expect(screen.getByLabelText(enSettings.zoho.connection.client_secret_label)).toBeTruthy();
    expect(screen.getByLabelText(enSettings.zoho.connection.refresh_token_label)).toBeTruthy();
    expect(screen.getByText(enSettings.zoho.connection.save)).toBeTruthy();
  });

  it("hides the manage form and module-sync card for plain members", () => {
    membersRef.current = [{ user_id: "user-1", role: "member" }];
    connectionRef.current = {
      configured: true,
      dc: "eu",
      client_id: "1000.abc",
      probe_status: "ok",
    };
    renderTab();
    // Status is still member-visible.
    expect(screen.getByText("1000.abc")).toBeTruthy();
    expect(screen.getByText(enSettings.zoho.probe.ok)).toBeTruthy();
    // No credential inputs, no disconnect, no CRM module card.
    expect(screen.queryByLabelText(enSettings.zoho.connection.client_id_label)).toBeNull();
    expect(screen.queryByLabelText(enSettings.zoho.connection.disconnect)).toBeNull();
    expect(screen.queryByText(enSettings.zoho.modules.title)).toBeNull();
  });

  it("saves credentials with the trimmed payload", async () => {
    mockSaveConnection.mockResolvedValue({ configured: true });
    renderTab();
    await userEvent.type(
      screen.getByLabelText(enSettings.zoho.connection.client_id_label),
      " 1000.abc ",
    );
    await userEvent.type(
      screen.getByLabelText(enSettings.zoho.connection.client_secret_label),
      "s3cret",
    );
    await userEvent.type(
      screen.getByLabelText(enSettings.zoho.connection.refresh_token_label),
      "1000.refresh",
    );
    await userEvent.selectOptions(
      screen.getByLabelText(enSettings.zoho.connection.dc_label),
      "eu",
    );
    await userEvent.click(screen.getByText(enSettings.zoho.connection.save));
    await waitFor(() => expect(mockSaveConnection).toHaveBeenCalled());
    expect(mockSaveConnection).toHaveBeenCalledWith({
      dc: "eu",
      client_id: "1000.abc",
      client_secret: "s3cret",
      refresh_token: "1000.refresh",
      scopes: undefined,
      crm_org_id: undefined,
      desk_org_id: undefined,
      projects_portal_id: undefined,
      sprints_team_id: undefined,
    });
    expect(toast.success).toHaveBeenCalled();
  });

  it("surfaces a 422 as the invalid-credentials message", async () => {
    mockSaveConnection.mockRejectedValue(new ApiError("unprocessable", 422));
    renderTab();
    await userEvent.type(
      screen.getByLabelText(enSettings.zoho.connection.client_id_label),
      "1000.abc",
    );
    await userEvent.type(
      screen.getByLabelText(enSettings.zoho.connection.client_secret_label),
      "bad",
    );
    await userEvent.type(
      screen.getByLabelText(enSettings.zoho.connection.refresh_token_label),
      "bad",
    );
    await userEvent.click(screen.getByText(enSettings.zoho.connection.save));
    expect(
      await screen.findByText(enSettings.zoho.connection.error_invalid_credentials),
    ).toBeTruthy();
  });

  it("surfaces a 503 as the sealing-key message", async () => {
    mockSaveConnection.mockRejectedValue(new ApiError("unavailable", 503));
    renderTab();
    await userEvent.type(
      screen.getByLabelText(enSettings.zoho.connection.client_id_label),
      "1000.abc",
    );
    await userEvent.type(
      screen.getByLabelText(enSettings.zoho.connection.client_secret_label),
      "x",
    );
    await userEvent.type(
      screen.getByLabelText(enSettings.zoho.connection.refresh_token_label),
      "y",
    );
    await userEvent.click(screen.getByText(enSettings.zoho.connection.save));
    expect(
      await screen.findByText(enSettings.zoho.connection.error_sealing_unavailable),
    ).toBeTruthy();
  });
});

describe("ZohoTab — personal binding section", () => {
  it("walks the unbound → bound flow", async () => {
    connectionRef.current = { configured: true, dc: "us", client_id: "1000.abc" };
    bindingRef.current = { bound: false };
    mockBind.mockResolvedValue({ bound: true, zoho_user_email: "j@x.io" });

    const { rerender } = renderTab();
    const input = screen.getByLabelText(enSettings.zoho.binding.grant_code_label);
    await userEvent.type(input, "1000.grant.code");
    await userEvent.click(screen.getByText(enSettings.zoho.binding.connect));
    await waitFor(() => expect(mockBind).toHaveBeenCalledWith("1000.grant.code"));
    expect(toast.success).toHaveBeenCalled();

    // The mutation invalidates the binding query; simulate the refetched state.
    bindingRef.current = { bound: true, zoho_user_email: "j@x.io", probe_status: "ok" };
    rerender(<ZohoTab />);
    expect(screen.getByText("Connected as j@x.io")).toBeTruthy();
    expect(screen.getByLabelText(enSettings.zoho.binding.unbind)).toBeTruthy();
    expect(
      screen.queryByLabelText(enSettings.zoho.binding.grant_code_label),
    ).toBeNull();
  });

  it("asks for the workspace connection before binding", () => {
    connectionRef.current = { configured: false };
    renderTab();
    expect(screen.getByText(enSettings.zoho.binding.requires_connection)).toBeTruthy();
    expect(
      screen.queryByLabelText(enSettings.zoho.binding.grant_code_label),
    ).toBeNull();
  });

  it("surfaces a 422 grant rejection inline", async () => {
    connectionRef.current = { configured: true, dc: "us", client_id: "1000.abc" };
    mockBind.mockRejectedValue(new ApiError("zoho_grant_invalid", 422));
    renderTab();
    await userEvent.type(
      screen.getByLabelText(enSettings.zoho.binding.grant_code_label),
      "stale-code",
    );
    await userEvent.click(screen.getByText(enSettings.zoho.binding.connect));
    expect(
      await screen.findByText(enSettings.zoho.binding.error_grant_invalid),
    ).toBeTruthy();
  });

  it("surfaces a 400 with the server's message inline", async () => {
    connectionRef.current = { configured: true, dc: "us", client_id: "1000.abc" };
    mockBind.mockRejectedValue(
      new ApiError("workspace zoho connection must be configured before binding user accounts", 400),
    );
    renderTab();
    await userEvent.type(
      screen.getByLabelText(enSettings.zoho.binding.grant_code_label),
      "code",
    );
    await userEvent.click(screen.getByText(enSettings.zoho.binding.connect));
    expect(
      await screen.findByText(
        "workspace zoho connection must be configured before binding user accounts",
      ),
    ).toBeTruthy();
  });

  it("unbinds from the bound state", async () => {
    connectionRef.current = { configured: true, dc: "us", client_id: "1000.abc" };
    bindingRef.current = { bound: true, zoho_user_email: "j@x.io", probe_status: "ok" };
    mockUnbind.mockResolvedValue(undefined);
    renderTab();
    await userEvent.click(screen.getByLabelText(enSettings.zoho.binding.unbind));
    await waitFor(() => expect(mockUnbind).toHaveBeenCalled());
  });
});

describe("ZohoTab — CRM module sync card", () => {
  it("shows the active-config count and navigates to the manager", async () => {
    connectionRef.current = { configured: true, dc: "us", client_id: "1000.abc" };
    configsRef.current = [
      { id: "c1", module_api_name: "Tasks", enabled: true },
      { id: "c2", module_api_name: "Calls", enabled: false },
    ];
    renderTab();
    expect(screen.getByText("Modules syncing: 1")).toBeTruthy();
    await userEvent.click(screen.getByText(enSettings.zoho.modules.manage));
    expect(mockPush).toHaveBeenCalledWith("/acme/zoho");
  });

  it("keeps the Projects/Sprints import deep link", async () => {
    renderTab();
    await userEvent.click(screen.getByText(enSettings.zoho.import.open));
    expect(mockPush).toHaveBeenCalledWith("/acme/zoho");
  });
});
