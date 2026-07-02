import { type ReactNode } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const mockCreate = vi.hoisted(() => vi.fn());
const mockUpdate = vi.hoisted(() => vi.fn());
const mockDelete = vi.hoisted(() => vi.fn());

type MemberRole = "owner" | "admin" | "member";

const membersRef = vi.hoisted(() => ({
  current: [{ user_id: "user-1", role: "owner" as MemberRole }],
}));
const connectionRef = vi.hoisted(() => ({
  current: { configured: true } as Record<string, unknown> | undefined,
}));
const modulesRef = vi.hoisted(() => ({
  current: [] as Record<string, unknown>[],
}));
const configsRef = vi.hoisted(() => ({
  current: [] as Record<string, unknown>[],
}));
const projectsRef = vi.hoisted(() => ({
  current: [{ id: "p1", title: "Billing" }, { id: "p2", title: "CS" }],
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[]; enabled?: boolean }) => {
    const base = {
      isLoading: false,
      isError: false,
      isFetching: false,
      refetch: vi.fn(),
    };
    if (opts.enabled === false) return { ...base, data: undefined };
    const key = JSON.stringify(opts.queryKey);
    if (key.includes("members")) return { ...base, data: membersRef.current };
    if (key.includes("zoho-connection")) return { ...base, data: connectionRef.current };
    if (key.includes("zoho-crm-modules")) return { ...base, data: modulesRef.current };
    if (key.includes("zoho-sync-configs")) return { ...base, data: configsRef.current };
    if (key.includes("projects")) return { ...base, data: projectsRef.current };
    return { ...base, data: undefined };
  },
  queryOptions: <T,>(opts: T) => opts,
}));

vi.mock("@agora/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@agora/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"], queryFn: vi.fn() }),
}));

vi.mock("@agora/core/projects/queries", () => ({
  projectListOptions: (wsId: string) => ({
    queryKey: ["projects", wsId, "list"],
    queryFn: vi.fn(),
  }),
}));

vi.mock("@agora/core/auth", () => {
  const useAuthStore = Object.assign(
    (sel?: (s: { user: { id: string } }) => unknown) =>
      sel ? sel({ user: { id: "user-1" } }) : { user: { id: "user-1" } },
    { getState: () => ({ user: { id: "user-1" } }) },
  );
  return { useAuthStore };
});

vi.mock("@agora/core/zoho", () => ({
  zohoConnectionOptions: (wsId: string) => ({
    queryKey: ["zoho-connection", wsId],
    queryFn: vi.fn(),
  }),
  zohoCRMModulesOptions: (wsId: string) => ({
    queryKey: ["zoho-crm-modules", wsId],
    queryFn: vi.fn(),
  }),
  zohoSyncConfigsOptions: (wsId: string) => ({
    queryKey: ["zoho-sync-configs", wsId],
    queryFn: vi.fn(),
  }),
  useCreateZohoSyncConfig: () => ({ mutateAsync: mockCreate, isPending: false }),
  useUpdateZohoSyncConfig: () => ({ mutateAsync: mockUpdate, isPending: false }),
  useDeleteZohoSyncConfig: () => ({ mutateAsync: mockDelete, isPending: false }),
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), message: vi.fn() },
}));

import { ZohoModuleSyncPanel } from "./zoho-module-sync-panel";

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

function renderPanel() {
  return render(<ZohoModuleSyncPanel />, { wrapper: I18nWrapper });
}

const tasksModule = {
  api_name: "Tasks",
  module_name: "Tasks",
  singular_label: "Task",
  plural_label: "Tasks",
  generated_type: "default",
  api_supported: true,
  creatable: true,
};

const customModule = {
  api_name: "CustomModule34",
  module_name: "Tickets",
  singular_label: "Ticket",
  plural_label: "Tickets",
  generated_type: "custom",
  api_supported: true,
  creatable: true,
};

const ticketsConfig = {
  id: "cfg-1",
  workspace_id: "workspace-1",
  connection_id: "conn-1",
  channel: "crm",
  module_api_name: "CustomModule34",
  project_id: "p2",
  enabled: true,
  direction: "in",
  field_map: { title: "Subject" },
  status_map: { in: { Open: "todo" }, out: {} },
  filter_coql: "",
  cursor: "",
  created_at: "2026-07-01T00:00:00Z",
  updated_at: "2026-07-01T00:00:00Z",
};

beforeEach(() => {
  vi.clearAllMocks();
  membersRef.current = [{ user_id: "user-1", role: "owner" }];
  connectionRef.current = { configured: true };
  modulesRef.current = [tasksModule, customModule];
  configsRef.current = [ticketsConfig];
});

describe("ZohoModuleSyncPanel — gating", () => {
  it("shows the admin-only note for plain members", () => {
    membersRef.current = [{ user_id: "user-1", role: "member" }];
    renderPanel();
    expect(screen.getByText(enSettings.zoho.modules.admin_only)).toBeTruthy();
    expect(screen.queryByText("Tasks")).toBeNull();
  });

  it("asks for the workspace connection when unconfigured", () => {
    connectionRef.current = { configured: false };
    renderPanel();
    expect(
      screen.getByText(enSettings.zoho.modules.requires_connection),
    ).toBeTruthy();
  });
});

describe("ZohoModuleSyncPanel — module list", () => {
  it("badges modules with their type and syncing state", () => {
    renderPanel();
    expect(screen.getByText("Tasks")).toBeTruthy();
    expect(screen.getByText("Tickets")).toBeTruthy();
    expect(screen.getByText(enSettings.zoho.modules.badge_default)).toBeTruthy();
    expect(screen.getByText(enSettings.zoho.modules.badge_custom)).toBeTruthy();
    // Only the module with a config carries the syncing badge.
    expect(screen.getAllByText(enSettings.zoho.modules.badge_syncing)).toHaveLength(1);
  });

  it("shows the empty note when discovery returns nothing", () => {
    modulesRef.current = [];
    configsRef.current = [];
    renderPanel();
    expect(screen.getByText(enSettings.zoho.modules.empty)).toBeTruthy();
  });
});

describe("ZohoModuleSyncPanel — create config", () => {
  it("submits the create payload with defaults and the chosen project", async () => {
    mockCreate.mockResolvedValue({ id: "cfg-2" });
    renderPanel();
    await userEvent.click(screen.getByText("Tasks"));
    await userEvent.selectOptions(
      screen.getByLabelText(enSettings.zoho.modules.project_label),
      "p1",
    );
    await userEvent.click(screen.getByText(enSettings.zoho.modules.create));
    await waitFor(() => expect(mockCreate).toHaveBeenCalled());
    expect(mockCreate).toHaveBeenCalledWith({
      module_api_name: "Tasks",
      direction: "both",
      project_id: "p1",
      field_map: { title: "Subject", status: "Status" },
      status_map: { in: {}, out: {} },
    });
  });

  it("blocks submit on invalid JSON in the field map", async () => {
    renderPanel();
    await userEvent.click(screen.getByText("Tasks"));
    fireEvent.change(
      screen.getByLabelText(enSettings.zoho.modules.field_map_label),
      { target: { value: "{not json" } },
    );
    await userEvent.click(screen.getByText(enSettings.zoho.modules.create));
    expect(screen.getByText(enSettings.zoho.modules.invalid_json)).toBeTruthy();
    expect(mockCreate).not.toHaveBeenCalled();
  });

  it("blocks submit when the map parses to a non-object", async () => {
    renderPanel();
    await userEvent.click(screen.getByText("Tasks"));
    fireEvent.change(
      screen.getByLabelText(enSettings.zoho.modules.status_map_label),
      { target: { value: '["not", "an", "object"]' } },
    );
    await userEvent.click(screen.getByText(enSettings.zoho.modules.create));
    expect(screen.getByText(enSettings.zoho.modules.invalid_json)).toBeTruthy();
    expect(mockCreate).not.toHaveBeenCalled();
  });
});

describe("ZohoModuleSyncPanel — edit config", () => {
  it("seeds the form from the existing config and submits an update", async () => {
    mockUpdate.mockResolvedValue({ id: "cfg-1" });
    renderPanel();
    await userEvent.click(screen.getByText("Tickets"));
    // Edit mode: update + delete affordances, no create button.
    expect(screen.queryByText(enSettings.zoho.modules.create)).toBeNull();
    const directionSelect = screen.getByLabelText<HTMLSelectElement>(
      enSettings.zoho.modules.direction_label,
    );
    expect(directionSelect.value).toBe("in");
    await userEvent.selectOptions(directionSelect, "both");
    await userEvent.click(screen.getByText(enSettings.zoho.modules.update));
    await waitFor(() => expect(mockUpdate).toHaveBeenCalled());
    expect(mockUpdate).toHaveBeenCalledWith({
      configId: "cfg-1",
      req: {
        direction: "both",
        project_id: "p2",
        enabled: true,
        field_map: { title: "Subject" },
        status_map: { in: { Open: "todo" }, out: {} },
      },
    });
  });

  it("deletes after confirming", async () => {
    mockDelete.mockResolvedValue(undefined);
    renderPanel();
    await userEvent.click(screen.getByText("Tickets"));
    await userEvent.click(screen.getByText(enSettings.zoho.modules.delete));
    // AlertDialog confirm step.
    await userEvent.click(
      await screen.findByText(
        enSettings.zoho.modules.delete,
        { selector: "[data-slot='alert-dialog-action']" },
      ),
    );
    await waitFor(() => expect(mockDelete).toHaveBeenCalledWith("cfg-1"));
  });
});
