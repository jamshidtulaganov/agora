import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const mockBeginInstall = vi.hoisted(() => vi.fn());
const mockDeleteInstall = vi.hoisted(() => vi.fn());
const mockSaveRoute = vi.hoisted(() => vi.fn());
const mockDeleteRoute = vi.hoisted(() => vi.fn());
const mockOpen = vi.hoisted(() => vi.fn());

type MemberRole = "owner" | "admin" | "member" | "guest";

const membersRef = vi.hoisted(() => ({
  current: [{ user_id: "user-1", role: "owner" as MemberRole }],
}));
const installationsRef = vi.hoisted(() => ({
  current: { installations: [] as unknown[], configured: true },
}));
const routesRef = vi.hoisted(() => ({
  current: {
    routes: [] as unknown[],
    available_events: ["failed", "qa_verdict", "review_verdict", "created"],
    default_events: ["failed", "qa_verdict", "review_verdict"],
  },
}));
const channelsRef = vi.hoisted(() => ({
  current: {
    channels: [{ id: "C1", name: "eng", is_private: false, is_member: true }],
    next_cursor: "",
    installation_id: "inst-1",
  },
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[]; enabled?: boolean }) => {
    if (opts.enabled === false) return { data: undefined, isLoading: false, isError: false };
    const key = JSON.stringify(opts.queryKey);
    if (key.includes("members")) return { data: membersRef.current, isLoading: false, isError: false };
    if (key.includes("installations")) {
      return { data: installationsRef.current, isLoading: false, isError: false };
    }
    if (key.includes("routes")) return { data: routesRef.current, isLoading: false, isError: false };
    if (key.includes("channels")) return { data: channelsRef.current, isLoading: false, isError: false };
    return { data: undefined, isLoading: false, isError: false };
  },
  queryOptions: <T,>(opts: T) => opts,
}));

vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "workspace-1" }));
vi.mock("@agora/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"], queryFn: vi.fn() }),
}));
vi.mock("@agora/core/auth", () => ({
  useAuthStore: (selector: (s: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "user-1" } }),
}));
vi.mock("@agora/core/slack", () => ({
  slackInstallationsOptions: () => ({ queryKey: ["slack", "ws", "installations"] }),
  slackRoutesOptions: () => ({ queryKey: ["slack", "ws", "routes"] }),
  slackChannelsOptions: () => ({ queryKey: ["slack", "ws", "channels", "inst-1"] }),
  useBeginSlackInstall: () => ({ mutateAsync: mockBeginInstall, isPending: false }),
  useDeleteSlackInstallation: () => ({ mutateAsync: mockDeleteInstall, isPending: false }),
  useSaveSlackRoute: () => ({ mutateAsync: mockSaveRoute, isPending: false }),
  useDeleteSlackRoute: () => ({ mutateAsync: mockDeleteRoute, isPending: false }),
  SLACK_ROUTE_EVENTS: ["failed", "qa_verdict", "review_verdict"],
  SLACK_DEFAULT_ROUTE_EVENTS: ["failed", "qa_verdict", "review_verdict"],
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const { SlackTab } = await import("./slack-tab");

function renderTab() {
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, settings: enSettings } }}>
      <SlackTab />
    </I18nProvider>,
  );
}

const INSTALL = {
  id: "inst-1",
  workspace_id: "workspace-1",
  team_id: "T0ACME",
  team_name: "Acme",
  app_id: "A0APP",
  bot_user_id: "U0BOT",
  scopes: ["chat:write"],
  installer_user_id: "user-1",
  status: "active",
  installed_at: "2026-09-19T09:00:00Z",
  updated_at: "2026-09-19T09:00:00Z",
};

const ROUTE = {
  id: "route-1",
  workspace_id: "workspace-1",
  installation_id: "inst-1",
  channel_id: "C1",
  channel_name: "eng",
  events: ["failed", "qa_verdict", "review_verdict"],
  enabled: true,
  created_at: "",
  updated_at: "",
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal("open", mockOpen);
  membersRef.current = [{ user_id: "user-1", role: "owner" }];
  installationsRef.current = { installations: [], configured: true };
  routesRef.current = {
    routes: [],
    available_events: ["failed", "qa_verdict", "review_verdict", "created"],
    default_events: ["failed", "qa_verdict", "review_verdict"],
  };
  channelsRef.current = {
    channels: [{ id: "C1", name: "eng", is_private: false, is_member: true }],
    next_cursor: "",
    installation_id: "inst-1",
  };
});

describe("SlackTab", () => {
  it("says the deployment cannot complete an install rather than offering one", () => {
    // Showing Connect without the four Slack keys means the admin discovers
    // the problem only after granting scopes in Slack.
    installationsRef.current = { installations: [], configured: false };
    renderTab();
    expect(screen.getByText(enSettings.slack.not_configured_title)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: enSettings.slack.connect_button })).toBeNull();
  });

  it("invites a connection when nothing is wired up yet", () => {
    renderTab();
    expect(screen.getByText(enSettings.slack.empty_title)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: enSettings.slack.connect_button })).toBeInTheDocument();
  });

  it("opens Slack's consent screen with the URL the server minted", async () => {
    mockBeginInstall.mockResolvedValue({ authorize_url: "https://slack.com/oauth/v2/authorize?x=1" });
    renderTab();

    await userEvent.click(screen.getByRole("button", { name: enSettings.slack.connect_button }));

    await waitFor(() => expect(mockBeginInstall).toHaveBeenCalled());
    expect(mockOpen).toHaveBeenCalledWith(
      "https://slack.com/oauth/v2/authorize?x=1",
      "_blank",
      "noopener,noreferrer",
    );
  });

  it("hides every management control from a plain member", () => {
    membersRef.current = [{ user_id: "user-1", role: "member" }];
    installationsRef.current = { installations: [INSTALL], configured: true };
    routesRef.current = { ...routesRef.current, routes: [ROUTE] };
    renderTab();

    expect(screen.getByText("Acme")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: enSettings.slack.connect_button })).toBeNull();
    expect(screen.queryByRole("button", { name: enSettings.slack.save })).toBeNull();
  });

  it("shows the connected team and its bot", () => {
    installationsRef.current = { installations: [INSTALL], configured: true };
    renderTab();
    expect(screen.getByText("Acme")).toBeInTheDocument();
    expect(screen.getByText(/U0BOT/)).toBeInTheDocument();
  });

  it("renders a revoked installation as revoked rather than hiding it", () => {
    installationsRef.current = {
      installations: [{ ...INSTALL, status: "revoked" }],
      configured: true,
    };
    renderTab();
    expect(screen.getByText(enSettings.slack.status_revoked)).toBeInTheDocument();
  });

  it("adds a channel on the quiet default set", async () => {
    installationsRef.current = { installations: [INSTALL], configured: true };
    mockSaveRoute.mockResolvedValue({});
    renderTab();

    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: enSettings.slack.channel_label }),
      "C1",
    );
    await userEvent.click(screen.getByRole("button", { name: enSettings.slack.add_route }));

    await waitFor(() => expect(mockSaveRoute).toHaveBeenCalled());
    expect(mockSaveRoute).toHaveBeenCalledWith({
      input: {
        installation_id: "inst-1",
        channel_id: "C1",
        channel_name: "eng",
        // The whole noise rule, asserted: a fresh route hears only the three
        // events that mean a human is needed.
        events: ["failed", "qa_verdict", "review_verdict"],
      },
    });
  });

  it("warns when the app is not in the channel yet", async () => {
    installationsRef.current = { installations: [INSTALL], configured: true };
    channelsRef.current = {
      ...channelsRef.current,
      channels: [{ id: "C1", name: "eng", is_private: false, is_member: false }],
    };
    renderTab();

    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: enSettings.slack.channel_label }),
      "C1",
    );
    expect(screen.getByText(enSettings.slack.not_member_hint)).toBeInTheDocument();
  });

  it("renders a route's current subscription and saves an edit", async () => {
    installationsRef.current = { installations: [INSTALL], configured: true };
    routesRef.current = { ...routesRef.current, routes: [ROUTE] };
    mockSaveRoute.mockResolvedValue({});
    renderTab();

    // The channel name appears twice — once as the route's heading and once
    // in the picker below it.
    expect(screen.getAllByText("#eng").length).toBeGreaterThan(0);
    const failed = screen.getByRole("checkbox", { name: enSettings.slack.events.failed });
    const created = screen.getByRole("checkbox", { name: enSettings.slack.events.created });
    expect(failed).toBeChecked();
    // Opt-in kinds start off — turning one on is the admin's decision.
    expect(created).not.toBeChecked();

    await userEvent.click(created);
    await userEvent.click(screen.getByRole("button", { name: enSettings.slack.save }));

    await waitFor(() => expect(mockSaveRoute).toHaveBeenCalled());
    expect(mockSaveRoute).toHaveBeenCalledWith({
      routeId: "route-1",
      input: { events: ["failed", "qa_verdict", "review_verdict", "created"] },
    });
  });

  it("offers an event kind this build has no label for, unchecked, as itself", () => {
    // Enum drift: a newer server ships a kind in available_events. It must
    // render — labelled with the raw kind rather than "Unknown" — instead of
    // being silently missing from the editor.
    installationsRef.current = { installations: [INSTALL], configured: true };
    routesRef.current = {
      routes: [ROUTE],
      available_events: ["failed", "qa_verdict", "review_verdict", "deploy_recorded"],
      default_events: ["failed"],
    };
    renderTab();

    const drifted = screen.getByRole("checkbox", { name: "deploy_recorded" });
    expect(drifted).toBeInTheDocument();
    expect(drifted).not.toBeChecked();
  });

  it("preserves an unknown kind the server already stored on the route", async () => {
    // The dangerous case: an older client editing one checkbox must not
    // delete routing it does not understand.
    installationsRef.current = { installations: [INSTALL], configured: true };
    routesRef.current = {
      routes: [{ ...ROUTE, events: ["failed", "deploy_recorded"] }],
      available_events: ["failed", "qa_verdict", "review_verdict"],
      default_events: ["failed"],
    };
    mockSaveRoute.mockResolvedValue({});
    renderTab();

    const drifted = screen.getByRole("checkbox", { name: "deploy_recorded" });
    expect(drifted).toBeChecked();

    await userEvent.click(screen.getByRole("checkbox", { name: enSettings.slack.events.qa_verdict }));
    await userEvent.click(screen.getByRole("button", { name: enSettings.slack.save }));

    await waitFor(() => expect(mockSaveRoute).toHaveBeenCalled());
    const saved = mockSaveRoute.mock.calls[0]?.[0] as { input: { events: string[] } };
    expect(saved.input.events).toContain("deploy_recorded");
    expect(saved.input.events).toContain("qa_verdict");
  });

  it("confirms before it stops posting to a channel", async () => {
    installationsRef.current = { installations: [INSTALL], configured: true };
    routesRef.current = { ...routesRef.current, routes: [ROUTE] };
    mockDeleteRoute.mockResolvedValue(undefined);
    renderTab();

    await userEvent.click(screen.getByRole("button", { name: enSettings.slack.remove }));
    expect(screen.getByText(enSettings.slack.remove_title)).toBeInTheDocument();
    expect(mockDeleteRoute).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: enSettings.slack.remove_confirm }));
    await waitFor(() => expect(mockDeleteRoute).toHaveBeenCalledWith("route-1"));
  });

  it("confirms before it disconnects a workspace", async () => {
    installationsRef.current = { installations: [INSTALL], configured: true };
    mockDeleteInstall.mockResolvedValue(undefined);
    renderTab();

    await userEvent.click(screen.getByRole("button", { name: enSettings.slack.disconnect }));
    expect(screen.getByText(enSettings.slack.disconnect_title)).toBeInTheDocument();
    expect(mockDeleteInstall).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: enSettings.slack.disconnect_confirm }));
    await waitFor(() => expect(mockDeleteInstall).toHaveBeenCalledWith("inst-1"));
  });
});
