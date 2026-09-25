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
const mockPush = vi.hoisted(() => vi.fn());

type MemberRole = "owner" | "admin" | "member";

const membersRef = vi.hoisted(() => ({
  current: [{ user_id: "user-1", role: "owner" as MemberRole }],
}));
const connectionRef = vi.hoisted(() => ({
  current: { configured: false } as Record<string, unknown> | undefined,
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
  zohoSyncConfigsOptions: (wsId: string) => ({
    queryKey: ["zoho-sync-configs", wsId],
    queryFn: vi.fn(),
  }),
  useSaveZohoConnection: () => ({ mutateAsync: mockSaveConnection, isPending: false }),
  useDeleteZohoConnection: () => ({ mutateAsync: mockDeleteConnection, isPending: false }),
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), message: vi.fn() },
}));

const mockCopyText = vi.hoisted(() => vi.fn<(text: string) => Promise<boolean>>());
vi.mock("@agora/ui/lib/clipboard", () => ({ copyText: mockCopyText }));

// The personal account card has its own test; here we only check it is
// mounted under the connector with this workspace's id.
vi.mock("./zoho-account-card", () => ({
  ZohoAccountCard: ({ wsId }: { wsId: string }) => (
    <div data-testid="zoho-account-card">{wsId}</div>
  ),
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
  configsRef.current = [];
});

const copy = enSettings.zoho.connection;
const REDIRECT = "https://agora.example.com/api/integrations/zoho/callback";

const CONFIGURED = {
  configured: true,
  dc: "eu",
  client_id: "1000.abc",
  crm_org_id: "org-crm",
  desk_org_id: "",
  probe_status: "ok",
  redirect_uri: REDIRECT,
  has_sync_grant: false,
};

async function fillCredentials(clientId: string, secret: string) {
  await userEvent.type(screen.getByLabelText(copy.client_id_label), clientId);
  await userEvent.type(screen.getByLabelText(copy.client_secret_label), secret);
}

describe("ZohoTab — connector, owner/admin, not set up", () => {
  it("shows the setup steps with the redirect URI to copy", async () => {
    connectionRef.current = { configured: false, redirect_uri: REDIRECT };
    mockCopyText.mockResolvedValue(true);
    renderTab();

    expect(screen.getByText(copy.title)).toBeTruthy();
    expect(screen.getByText(copy.description_setup)).toBeTruthy();
    expect(screen.getByText(copy.step_create)).toBeTruthy();
    expect(screen.getByText(copy.step_redirect)).toBeTruthy();
    expect(screen.getByText(copy.step_credentials)).toBeTruthy();
    const field = screen.getByLabelText(copy.redirect_uri_label) as HTMLInputElement;
    expect(field.value).toBe(REDIRECT);
    expect(field.readOnly).toBe(true);

    await userEvent.click(screen.getByRole("button", { name: copy.copy }));
    expect(mockCopyText).toHaveBeenCalledWith(REDIRECT);
    expect(toast.success).toHaveBeenCalledWith(copy.copied);
  });

  it("says so when copying fails", async () => {
    connectionRef.current = { configured: false, redirect_uri: REDIRECT };
    mockCopyText.mockResolvedValue(false);
    renderTab();
    await userEvent.click(screen.getByRole("button", { name: copy.copy }));
    expect(toast.error).toHaveBeenCalledWith(copy.error_copy_failed);
  });

  it("skips the redirect step when the server doesn't send one", () => {
    connectionRef.current = { configured: false };
    renderTab();
    expect(screen.getByText(copy.step_create)).toBeTruthy();
    expect(screen.queryByText(copy.step_redirect)).toBeNull();
    expect(screen.queryByLabelText(copy.redirect_uri_label)).toBeNull();
    expect(screen.getByText(copy.step_credentials)).toBeTruthy();
  });

  it("saves with just Client ID and Client secret (refresh token optional)", async () => {
    mockSaveConnection.mockResolvedValue({ configured: true });
    renderTab();
    expect(screen.getByLabelText(copy.refresh_token_label)).toBeTruthy();
    expect(screen.getByText(copy.refresh_token_hint)).toBeTruthy();
    // The scopes field is gone.
    expect(screen.queryByLabelText(/scopes/i)).toBeNull();

    await fillCredentials(" 1000.abc ", "s3cret");
    await userEvent.selectOptions(screen.getByLabelText(copy.dc_label), "eu");
    await userEvent.click(screen.getByRole("button", { name: copy.save }));

    await waitFor(() => expect(mockSaveConnection).toHaveBeenCalled());
    expect(mockSaveConnection).toHaveBeenCalledWith({
      dc: "eu",
      client_id: "1000.abc",
      client_secret: "s3cret",
      refresh_token: undefined,
      crm_org_id: undefined,
      desk_org_id: undefined,
      projects_portal_id: undefined,
      sprints_team_id: undefined,
    });
    expect(toast.success).toHaveBeenCalledWith(copy.toast_saved);
  });

  it("sends the refresh token when one is given", async () => {
    mockSaveConnection.mockResolvedValue({ configured: true });
    renderTab();
    await fillCredentials("1000.abc", "s3cret");
    await userEvent.type(screen.getByLabelText(copy.refresh_token_label), " 1000.refresh ");
    await userEvent.click(screen.getByRole("button", { name: copy.save }));
    await waitFor(() => expect(mockSaveConnection).toHaveBeenCalled());
    expect(mockSaveConnection.mock.calls[0]?.[0]).toMatchObject({
      refresh_token: "1000.refresh",
    });
  });

  it("keeps Save disabled until Client ID and Client secret are filled", async () => {
    renderTab();
    const save = screen.getByRole("button", { name: copy.save }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    await userEvent.type(screen.getByLabelText(copy.client_id_label), "1000.abc");
    expect(save.disabled).toBe(true);
    await userEvent.type(screen.getByLabelText(copy.client_secret_label), "s3cret");
    expect(save.disabled).toBe(false);
  });

  it("surfaces a 422 as the invalid-credentials message", async () => {
    mockSaveConnection.mockRejectedValue(new ApiError("unprocessable", 422));
    renderTab();
    await fillCredentials("1000.abc", "bad");
    await userEvent.click(screen.getByRole("button", { name: copy.save }));
    expect(await screen.findByText(copy.error_invalid_credentials)).toBeTruthy();
  });

  it("surfaces a 503 as the sealing-key message", async () => {
    mockSaveConnection.mockRejectedValue(new ApiError("unavailable", 503));
    renderTab();
    await fillCredentials("1000.abc", "x");
    await userEvent.click(screen.getByRole("button", { name: copy.save }));
    expect(await screen.findByText(copy.error_sealing_unavailable)).toBeTruthy();
  });
});

describe("ZohoTab — connector, owner/admin, set up", () => {
  it("shows the saved connector, the redirect URI and the sync state", () => {
    connectionRef.current = CONFIGURED;
    renderTab();

    expect(screen.getByText(copy.description_ready)).toBeTruthy();
    expect(screen.getByText("1000.abc")).toBeTruthy();
    expect(screen.getByText("eu")).toBeTruthy();
    expect(screen.getByText(enSettings.zoho.probe.ok)).toBeTruthy();
    expect(
      (screen.getByLabelText(copy.redirect_uri_label) as HTMLInputElement).value,
    ).toBe(REDIRECT);
    expect(screen.getByText(copy.sync_off)).toBeTruthy();
    // No setup steps and no form until Edit.
    expect(screen.queryByText(copy.step_create)).toBeNull();
    expect(screen.queryByLabelText(copy.client_secret_label)).toBeNull();
  });

  it("says CRM sync is on when the sync grant is stored", () => {
    connectionRef.current = { ...CONFIGURED, has_sync_grant: true };
    renderTab();
    expect(screen.getByText(copy.sync_on)).toBeTruthy();
    expect(screen.queryByText(copy.sync_off)).toBeNull();
  });

  it("edits starting from the saved data center, Client ID and org IDs", async () => {
    connectionRef.current = CONFIGURED;
    mockSaveConnection.mockResolvedValue(CONFIGURED);
    renderTab();

    await userEvent.click(screen.getByRole("button", { name: copy.edit }));
    expect(
      (screen.getByLabelText(copy.client_id_label) as HTMLInputElement).value,
    ).toBe("1000.abc");
    expect((screen.getByLabelText(copy.dc_label) as HTMLSelectElement).value).toBe("eu");

    await userEvent.type(screen.getByLabelText(copy.client_secret_label), "new-secret");
    await userEvent.click(screen.getByRole("button", { name: copy.save }));

    await waitFor(() => expect(mockSaveConnection).toHaveBeenCalled());
    expect(mockSaveConnection).toHaveBeenCalledWith({
      dc: "eu",
      client_id: "1000.abc",
      client_secret: "new-secret",
      refresh_token: undefined,
      crm_org_id: "org-crm",
      desk_org_id: undefined,
      projects_portal_id: undefined,
      sprints_team_id: undefined,
    });
    // The form closes after a successful save.
    await waitFor(() =>
      expect(screen.queryByLabelText(copy.client_secret_label)).toBeNull(),
    );
  });

  it("warns that an empty refresh token stops sync when sync is on", async () => {
    connectionRef.current = { ...CONFIGURED, has_sync_grant: true };
    renderTab();
    await userEvent.click(screen.getByRole("button", { name: copy.edit }));
    expect(screen.getByText(copy.refresh_token_hint_replace)).toBeTruthy();
  });

  it("closes the form on Cancel without saving", async () => {
    connectionRef.current = CONFIGURED;
    renderTab();
    await userEvent.click(screen.getByRole("button", { name: copy.edit }));
    await userEvent.click(screen.getByRole("button", { name: copy.cancel }));
    expect(screen.queryByLabelText(copy.client_secret_label)).toBeNull();
    expect(mockSaveConnection).not.toHaveBeenCalled();
  });

  it("asks before removing the connector", async () => {
    connectionRef.current = CONFIGURED;
    mockDeleteConnection.mockResolvedValue(undefined);
    renderTab();

    await userEvent.click(screen.getByRole("button", { name: copy.disconnect }));
    expect(await screen.findByText(copy.disconnect_confirm_title)).toBeTruthy();
    expect(mockDeleteConnection).not.toHaveBeenCalled();

    const confirm = screen
      .getAllByRole("button", { name: copy.disconnect })
      .find((el) => el.getAttribute("data-slot") === "alert-dialog-action");
    await userEvent.click(confirm!);
    await waitFor(() => expect(mockDeleteConnection).toHaveBeenCalledTimes(1));
    expect(toast.success).toHaveBeenCalledWith(copy.toast_disconnected);
  });
});

describe("ZohoTab — connector, plain members", () => {
  it("shows one line when the connector is set up, and nothing to manage", () => {
    membersRef.current = [{ user_id: "user-1", role: "member" }];
    connectionRef.current = { ...CONFIGURED, has_sync_grant: true };
    renderTab();

    expect(screen.getByText(copy.member_ready)).toBeTruthy();
    expect(screen.queryByText("1000.abc")).toBeNull();
    expect(screen.queryByLabelText(copy.redirect_uri_label)).toBeNull();
    expect(screen.queryByLabelText(copy.client_id_label)).toBeNull();
    expect(screen.queryByRole("button", { name: copy.edit })).toBeNull();
    expect(screen.queryByRole("button", { name: copy.disconnect })).toBeNull();
    expect(screen.queryByText(enSettings.zoho.modules.title)).toBeNull();
  });

  it("asks for an owner or admin when the connector is not set up", () => {
    membersRef.current = [{ user_id: "user-1", role: "member" }];
    connectionRef.current = { configured: false, redirect_uri: REDIRECT };
    renderTab();

    expect(screen.getByText(copy.member_not_configured)).toBeTruthy();
    expect(screen.queryByText(copy.step_create)).toBeNull();
    expect(screen.queryByLabelText(copy.redirect_uri_label)).toBeNull();
    expect(screen.queryByLabelText(copy.client_id_label)).toBeNull();
  });
});

describe("ZohoTab — your Zoho account", () => {
  it("mounts the personal account card for this workspace", () => {
    renderTab();
    expect(screen.getByTestId("zoho-account-card").textContent).toBe("workspace-1");
  });
});

describe("ZohoTab — CRM module sync card", () => {
  it("is hidden until the connector has the sync grant", () => {
    connectionRef.current = CONFIGURED;
    renderTab();
    expect(screen.queryByText(enSettings.zoho.modules.title)).toBeNull();
  });

  it("shows the active-config count and navigates to the manager", async () => {
    connectionRef.current = { ...CONFIGURED, has_sync_grant: true };
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
