import { type ReactNode } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const mockList = vi.hoisted(() => vi.fn());
const mockCreate = vi.hoisted(() => vi.fn());
const mockDelete = vi.hoisted(() => vi.fn());
const mockInvalidate = vi.hoisted(() => vi.fn());

type MemberRole = "owner" | "admin" | "member";

const membersRef = vi.hoisted(() => ({
  current: [{ user_id: "user-1", role: "owner" as MemberRole }],
}));
const integrationsRef = vi.hoisted(() => ({
  current: [] as Record<string, unknown>[],
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[]; enabled?: boolean }) => {
    if (opts.enabled === false) return { data: undefined, isLoading: false };
    const key = JSON.stringify(opts.queryKey);
    if (key.includes("members")) return { data: membersRef.current, isLoading: false };
    if (key.includes("release-integrations")) return { data: integrationsRef.current, isLoading: false };
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
    listReleaseIntegrations: mockList,
    createReleaseIntegration: mockCreate,
    deleteReleaseIntegration: mockDelete,
  },
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

import { ReleaseIntegrationsSection } from "./release-integrations-section";
import { toast } from "sonner";

const TEST_RESOURCES = { en: { common: enCommon, settings: enSettings } };

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function renderSection() {
  return render(<ReleaseIntegrationsSection />, { wrapper: I18nWrapper });
}

beforeEach(() => {
  vi.clearAllMocks();
  membersRef.current = [{ user_id: "user-1", role: "owner" }];
  integrationsRef.current = [];
});

describe("ReleaseIntegrationsSection", () => {
  it("renders the empty state with an add form for admins", () => {
    renderSection();
    expect(screen.getByText(enSettings.release_integrations.not_configured)).toBeTruthy();
    expect(screen.getByLabelText(enSettings.release_integrations.url_label)).toBeTruthy();
    expect(screen.getByText(enSettings.release_integrations.add)).toBeTruthy();
  });

  it("hides the add form and remove button for plain members", () => {
    membersRef.current = [{ user_id: "user-1", role: "member" }];
    integrationsRef.current = [
      { id: "ri1", kind: "webhook", config: { name: "Release channel" }, events: ["deploy_recorded"], enabled: true, probe_status: "ok", has_secret: true },
    ];
    renderSection();
    expect(screen.getByText("Release channel")).toBeTruthy();
    expect(screen.queryByLabelText(enSettings.release_integrations.url_label)).toBeNull();
    expect(screen.queryByLabelText(enSettings.release_integrations.remove)).toBeNull();
  });

  it("lists a configured integration with its events and probe status", () => {
    integrationsRef.current = [
      { id: "ri1", kind: "webhook", config: { name: "Release channel" }, events: ["deploy_recorded", "release_shipped"], enabled: true, probe_status: "ok", has_secret: true },
    ];
    renderSection();
    expect(screen.getByText("Release channel")).toBeTruthy();
    expect(screen.getByText(enSettings.release_integrations.status_ok)).toBeTruthy();
  });

  it("creates a webhook integration and refreshes the list", async () => {
    mockCreate.mockResolvedValue(undefined);
    renderSection();
    await userEvent.type(
      screen.getByLabelText(enSettings.release_integrations.url_label),
      "https://hooks.example.com/x",
    );
    await userEvent.click(screen.getByText(enSettings.release_integrations.add));
    await waitFor(() => expect(mockCreate).toHaveBeenCalled());
    const [wsId, body] = mockCreate.mock.calls[0]!;
    expect(wsId).toBe("workspace-1");
    expect(body.url).toBe("https://hooks.example.com/x");
    // Both events are checked by default.
    expect(body.events).toEqual(["deploy_recorded", "release_shipped"]);
    expect(mockInvalidate).toHaveBeenCalled();
    expect(toast.success).toHaveBeenCalled();
  });

  it("switches to GitHub Release and creates with owner/repo/token", async () => {
    mockCreate.mockResolvedValue(undefined);
    renderSection();
    await userEvent.selectOptions(
      screen.getByLabelText(enSettings.release_integrations.kind_label),
      "github_release",
    );
    await userEvent.type(screen.getByLabelText(enSettings.release_integrations.owner_label), "octocat");
    await userEvent.type(screen.getByLabelText(enSettings.release_integrations.repo_label), "hello");
    await userEvent.type(screen.getByLabelText(enSettings.release_integrations.token_label), "ghp_secret");
    await userEvent.click(screen.getByText(enSettings.release_integrations.add));
    await waitFor(() => expect(mockCreate).toHaveBeenCalled());
    const [, body] = mockCreate.mock.calls[0]!;
    expect(body.kind).toBe("github_release");
    expect(body.owner).toBe("octocat");
    expect(body.repo).toBe("hello");
    expect(body.token).toBe("ghp_secret");
    // The webhook-only field is not part of a github_release payload.
    expect(body.url).toBeUndefined();
  });

  it("creates a Bitrix integration with no secret field", async () => {
    mockCreate.mockResolvedValue(undefined);
    renderSection();
    await userEvent.selectOptions(
      screen.getByLabelText(enSettings.release_integrations.kind_label),
      "bitrix",
    );
    // Bitrix shows an explanatory note, no secret input.
    expect(screen.getByText(enSettings.release_integrations.bitrix_note)).toBeTruthy();
    expect(screen.queryByLabelText(enSettings.release_integrations.token_label)).toBeNull();
    await userEvent.click(screen.getByText(enSettings.release_integrations.add));
    await waitFor(() => expect(mockCreate).toHaveBeenCalled());
    const [, body] = mockCreate.mock.calls[0]!;
    expect(body.kind).toBe("bitrix");
  });

  it("shows a kind badge and config summary for a github_release row", () => {
    integrationsRef.current = [
      {
        id: "ri9",
        kind: "github_release",
        config: { name: "Prod release", owner: "octo", repo: "hello" },
        events: ["release_shipped"],
        enabled: true,
        probe_status: "ok",
        has_secret: true,
      },
    ];
    renderSection();
    // The label appears both as the row's kind badge and as a select option, so
    // assert at least one match plus the unique owner/repo config summary.
    expect(screen.getAllByText(enSettings.release_integrations.kind_github_release).length).toBeGreaterThan(0);
    expect(screen.getByText("octo/hello")).toBeTruthy();
  });

  it("removes an integration after confirming in the dialog", async () => {
    integrationsRef.current = [
      { id: "ri1", kind: "webhook", config: { name: "Release channel" }, events: ["deploy_recorded"], enabled: true, probe_status: "ok", has_secret: true },
    ];
    mockDelete.mockResolvedValue(undefined);
    renderSection();
    await userEvent.click(screen.getByLabelText(enSettings.release_integrations.remove));
    // Confirm dialog appears; click its destructive action.
    const confirm = await screen.findByText(enSettings.release_integrations.confirm_remove);
    await userEvent.click(confirm);
    await waitFor(() => expect(mockDelete).toHaveBeenCalledWith("workspace-1", "ri1"));
    expect(mockInvalidate).toHaveBeenCalled();
  });
});
