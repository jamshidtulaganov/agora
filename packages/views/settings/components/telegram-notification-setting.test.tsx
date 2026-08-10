import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const mockStart = vi.hoisted(() => vi.fn());
const mockInvalidate = vi.hoisted(() => vi.fn());
const linksRef = vi.hoisted(() => ({
  current: { links: [] as { provider: string; external_id: string }[] },
}));
const configRef = vi.hoisted(() => ({
  telegramBotUsername: "agora_bot",
  telegramBotsEnabled: true,
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: linksRef.current, isLoading: false }),
  useQueryClient: () => ({ invalidateQueries: mockInvalidate }),
}));

vi.mock("@agora/core/api", () => ({
  api: {
    listMyExternalLinks: vi.fn(),
    startTelegramLink: mockStart,
    verifyTelegramLink: vi.fn(),
    unlinkTelegramIdentity: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(message: string, status: number) {
      super(message);
      this.status = status;
    }
  },
}));

vi.mock("@agora/core/config", () => ({
  useConfigStore: (selector: (s: typeof configRef) => unknown) => selector(configRef),
}));

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

vi.mock("../../navigation", () => ({
  useNavigation: () => ({ push: vi.fn() }),
}));

vi.mock("@agora/core/paths", () => ({
  useWorkspacePaths: () => ({ settings: () => "/acme/settings" }),
}));

const { TelegramNotificationSetting } = await import("./telegram-notification-setting");

function renderSetting() {
  return render(
    <I18nProvider
      locale="en"
      resources={{ en: { common: enCommon, settings: enSettings } }}
    >
      <TelegramNotificationSetting />
    </I18nProvider>,
  );
}

describe("TelegramNotificationSetting", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    linksRef.current = { links: [] };
    configRef.telegramBotUsername = "agora_bot";
    configRef.telegramBotsEnabled = true;
  });

  it("renders nothing when the platform bot is not configured", () => {
    configRef.telegramBotUsername = "";
    const { container } = renderSetting();
    expect(container).toBeEmptyDOMElement();
  });

  it("shows connect when Telegram is not linked", () => {
    renderSetting();
    expect(
      screen.getByRole("button", { name: enSettings.notifications.telegram.connect }),
    ).toBeInTheDocument();
  });

  it("keeps the group setup link discoverable when per-agent bots are not configured", () => {
    configRef.telegramBotsEnabled = false;
    renderSetting();
    expect(
      screen.getByRole("button", { name: enSettings.notifications.telegram.groups_link }),
    ).toBeInTheDocument();
  });

  it("shows unlink when Telegram is linked", () => {
    linksRef.current = { links: [{ provider: "telegram", external_id: "99" }] };
    renderSetting();
    expect(
      screen.getByRole("button", { name: enSettings.notifications.telegram.unlink }),
    ).toBeInTheDocument();
  });

  it("starts the link flow and opens the code dialog", async () => {
    mockStart.mockResolvedValue({
      nonce: "n1",
      deep_link: "https://t.me/agora_bot?start=login_n1",
    });
    const openSpy = vi.spyOn(window, "open").mockImplementation(() => null);

    renderSetting();
    await userEvent.click(
      screen.getByRole("button", { name: enSettings.notifications.telegram.connect }),
    );

    expect(mockStart).toHaveBeenCalled();
    expect(openSpy).toHaveBeenCalledWith(
      "https://t.me/agora_bot?start=login_n1",
      "_blank",
      "noopener,noreferrer",
    );
    expect(
      screen.getByRole("heading", { name: enSettings.notifications.telegram.dialog_title }),
    ).toBeInTheDocument();
    openSpy.mockRestore();
  });
});
