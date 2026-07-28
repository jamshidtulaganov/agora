import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const mockInstall = vi.hoisted(() => vi.fn());
const mockDelete = vi.hoisted(() => vi.fn());
const mockSetAccess = vi.hoisted(() => vi.fn());
const mockBindLink = vi.hoisted(() => vi.fn());
const mockInvalidate = vi.hoisted(() => vi.fn());

type MemberRole = "owner" | "admin" | "member" | "guest";

const membersRef = vi.hoisted(() => ({
  current: [{ user_id: "user-1", role: "owner" as MemberRole }],
}));
const installationsRef = vi.hoisted(() => ({
  current: { installations: [] as unknown[], configured: true },
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[]; enabled?: boolean }) => {
    if (opts.enabled === false) return { data: undefined, isLoading: false };
    const key = JSON.stringify(opts.queryKey);
    if (key.includes("members")) return { data: membersRef.current, isLoading: false };
    if (key.includes("telegram-install-picker")) {
      return { data: [{ id: "agent-2", name: "sd-bridge-lead" }], isLoading: false };
    }
    if (key.includes("installations")) return { data: installationsRef.current, isLoading: false };
    return { data: undefined, isLoading: false };
  },
  useQueryClient: () => ({ invalidateQueries: mockInvalidate }),
  queryOptions: <T,>(opts: T) => opts,
}));

vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "workspace-1" }));
vi.mock("@agora/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"], queryFn: vi.fn() }),
}));
vi.mock("@agora/core/workspace/hooks", () => ({
  useActorName: () => ({
    getAgentName: (id: string) => (id === "agent-1" ? "bitrix-manager" : "Unknown Agent"),
    getMemberName: () => "Unknown",
    getSquadName: () => "Unknown Squad",
    getActorName: () => "Unknown",
    getActorInitials: () => "??",
    getActorAvatarUrl: () => null,
  }),
}));
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorType, actorId }: { actorType: string; actorId: string }) => (
    <span data-testid="actor-avatar" data-actor-type={actorType} data-actor-id={actorId} />
  ),
}));
vi.mock("@agora/core/telegram", () => ({
  telegramInstallationsOptions: () => ({
    queryKey: ["telegram", "installations"],
    queryFn: vi.fn(),
  }),
  telegramKeys: { installations: (wsId: string) => ["telegram", "installations", wsId] },
}));
vi.mock("@agora/core/api", () => ({
  api: {
    installAgentTelegramBot: mockInstall,
    deleteAgentTelegramBot: mockDelete,
    setAgentTelegramAccess: mockSetAccess,
    createAgentTelegramBindLink: mockBindLink,
    listAgents: vi.fn(),
  },
}));
vi.mock("@agora/core/auth", () => ({
  useAuthStore: (selector: (s: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "user-1" } }),
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
// react-qr-code renders an <svg> built from a canvas-free encoder, but the
// component is irrelevant to what these tests assert and pulls CJS interop
// into jsdom. A stub keeps the QR step assertable by its value alone.
vi.mock("react-qr-code", () => ({
  QRCode: ({ value }: { value: string }) => <span data-testid="qr" data-value={value} />,
}));

const { TelegramTab } = await import("./telegram-tab");

function renderTab() {
  return render(
    <I18nProvider
      locale="en"
      resources={{ en: { common: enCommon, settings: enSettings } }}
    >
      <TelegramTab />
    </I18nProvider>,
  );
}

const CONNECTED = {
  agent_id: "agent-1",
  bot_username: "sd_pm_agent_bot",
  bot_user_id: "8935986908",
  status: "active",
  access_policy: "allowlist",
  allowed_user_ids: ["905434593"],
  allowed_chat_ids: ["-1004336001519"],
};

beforeEach(() => {
  vi.clearAllMocks();
  membersRef.current = [{ user_id: "user-1", role: "owner" }];
  installationsRef.current = { installations: [], configured: true };
});

describe("TelegramTab", () => {
  it("tells the operator when the deployment cannot complete an install", () => {
    // Offering the form without the seal key means the operator discovers the
    // problem only after pasting a live bot token.
    installationsRef.current = { installations: [], configured: false };
    renderTab();
    expect(screen.getByText(enSettings.telegram.not_configured_title)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: enSettings.telegram.connect_button })).toBeNull();
  });

  it("points at BotFather when nothing is connected yet", () => {
    renderTab();
    expect(screen.getByText(enSettings.telegram.empty_title)).toBeInTheDocument();
  });

  it("shows the owning agent, the bot handle, its policy and group count", () => {
    installationsRef.current = { installations: [CONNECTED], configured: true };
    renderTab();
    expect(screen.getByText("bitrix-manager")).toBeInTheDocument();
    expect(screen.getByText(/@sd_pm_agent_bot/)).toBeInTheDocument();
    expect(screen.getByText(new RegExp(enSettings.telegram.policy_allowlist))).toBeInTheDocument();
    expect(screen.getByText(/1 group/)).toBeInTheDocument();
  });

  it("renders an unknown policy generically instead of blank", () => {
    // A newer server can introduce a policy this build has no label for; the
    // row must downgrade rather than show an empty gap.
    installationsRef.current = {
      installations: [{ ...CONNECTED, access_policy: "invite_only" }],
      configured: true,
    };
    renderTab();
    expect(screen.getByText(new RegExp(enSettings.telegram.policy_unknown))).toBeInTheDocument();
  });

  it("hides every management control from a plain member", () => {
    // The backend refuses them anyway; showing a button that will be refused
    // is worse than not showing it.
    membersRef.current = [{ user_id: "user-1", role: "member" }];
    installationsRef.current = { installations: [CONNECTED], configured: true };
    renderTab();
    expect(screen.queryByRole("button", { name: enSettings.telegram.connect_button })).toBeNull();
    expect(screen.queryByRole("button", { name: enSettings.telegram.add_group })).toBeNull();
    expect(screen.queryByRole("button", { name: enSettings.telegram.manage_access })).toBeNull();
  });

  it("mints a bind link only on request and shows it as a QR", async () => {
    // The token is single-use and short-lived, so it is minted when the
    // operator is ready to scan — not when the dialog opens.
    mockBindLink.mockResolvedValue({
      group_url: "https://t.me/sd_pm_agent_bot?startgroup=bind_abc",
      bot_username: "sd_pm_agent_bot",
      expires_at: "2026-07-28T14:00:00Z",
    });
    installationsRef.current = { installations: [CONNECTED], configured: true };
    renderTab();
    await userEvent.click(screen.getByRole("button", { name: enSettings.telegram.add_group }));
    expect(mockBindLink).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: enSettings.telegram.bind_generate }));
    await waitFor(() => {
      expect(screen.getByTestId("qr")).toHaveAttribute(
        "data-value",
        "https://t.me/sd_pm_agent_bot?startgroup=bind_abc",
      );
    });
  });

  it("submits access edits with ids split from free text", async () => {
    // People paste ids separated by newlines, commas or spaces; a trailing
    // newline must not be submitted as an id the backend then rejects.
    mockSetAccess.mockResolvedValue(CONNECTED);
    installationsRef.current = { installations: [CONNECTED], configured: true };
    renderTab();
    await userEvent.click(screen.getByRole("button", { name: enSettings.telegram.manage_access }));

    const chats = screen.getByLabelText(enSettings.telegram.access_chats_label);
    await userEvent.clear(chats);
    await userEvent.type(chats, "-1001, -1002\n");
    await userEvent.click(screen.getByRole("button", { name: enSettings.telegram.access_save }));

    await waitFor(() => {
      expect(mockSetAccess).toHaveBeenCalledWith("workspace-1", "agent-1", {
        policy: "allowlist",
        allowed_user_ids: ["905434593"],
        allowed_chat_ids: ["-1001", "-1002"],
      });
    });
  });

  it("only offers agents that do not already own a bot", async () => {
    // The backend keys the installation on agent_id, so picking an occupied
    // agent would silently replace its bot instead of adding one.
    installationsRef.current = { installations: [CONNECTED], configured: true };
    renderTab();
    await userEvent.click(screen.getByRole("button", { name: enSettings.telegram.connect_button }));
    await userEvent.click(screen.getByRole("combobox"));
    await waitFor(() => {
      expect(screen.getByRole("option", { name: "sd-bridge-lead" })).toBeInTheDocument();
    });
    expect(screen.queryByRole("option", { name: "bitrix-manager" })).toBeNull();
  });

  it("sends the pasted token once and closes the form", async () => {
    // The token is full control of the bot, so the form must not linger with
    // it still on screen after a successful install.
    mockInstall.mockResolvedValue(CONNECTED);
    renderTab();
    await userEvent.click(screen.getByRole("button", { name: enSettings.telegram.connect_button }));
    await userEvent.click(screen.getByRole("combobox"));
    await waitFor(() => screen.getByRole("option", { name: "sd-bridge-lead" }));
    await userEvent.click(screen.getByRole("option", { name: "sd-bridge-lead" }));

    const token = screen.getByLabelText(enSettings.telegram.install_token_label);
    await userEvent.type(token, "123:AA-secret");
    await userEvent.click(screen.getByRole("button", { name: enSettings.telegram.install_submit }));

    await waitFor(() => {
      expect(mockInstall).toHaveBeenCalledWith("workspace-1", "agent-2", "123:AA-secret");
    });
    // The dialog closes, which is what actually takes the token off screen —
    // the explicit state clear only covers the window before unmount.
    await waitFor(() => {
      expect(screen.queryByLabelText(enSettings.telegram.install_token_label)).toBeNull();
    });
  });
});
