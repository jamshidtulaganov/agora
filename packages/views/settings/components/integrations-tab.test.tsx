import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const configRef = vi.hoisted(() => ({
  bitrixEnabled: false,
  zohoEnabled: false,
  larkEnabled: false,
  telegramBotsEnabled: false,
}));
const queryCalls = vi.hoisted(() => [] as { queryKey?: unknown; enabled?: boolean }[]);
const navigationRef = vi.hoisted(() => ({ search: "" }));

vi.mock("@agora/core/config", () => ({
  useConfigStore: (selector: (state: typeof configRef) => unknown) => selector(configRef),
}));
vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "workspace-1" }));
vi.mock("../../navigation", () => ({
  useNavigation: () => ({
    pathname: "/acme/settings",
    searchParams: new URLSearchParams(navigationRef.search),
  }),
}));
vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey?: unknown; enabled?: boolean }) => {
    queryCalls.push(options);
    return { data: undefined };
  },
  queryOptions: <T,>(options: T) => options,
}));
vi.mock("@agora/core/api", () => ({
  api: {
    getFigmaCredentialStatus: vi.fn(),
    listReleaseIntegrations: vi.fn(),
  },
}));
vi.mock("@agora/core/zoho", () => ({
  zohoConnectionOptions: () => ({ queryKey: ["zoho"] }),
}));
vi.mock("@agora/core/lark", () => ({
  larkInstallationsOptions: () => ({ queryKey: ["lark"] }),
}));
vi.mock("@agora/core/telegram", () => ({
  telegramInstallationsOptions: () => ({ queryKey: ["telegram", "workspace-1", "installations"] }),
}));
vi.mock("./mcp-servers-tab", () => ({ McpServersTab: () => null }));
vi.mock("./release-integrations-section", () => ({ ReleaseIntegrationsSection: () => null }));
vi.mock("./figma-integration-section", () => ({ FigmaIntegrationSection: () => null }));
vi.mock("./bitrix-tab", () => ({ BitrixTab: () => null }));
vi.mock("./zoho-tab", () => ({ ZohoTab: () => null }));
vi.mock("./lark-tab", () => ({ LarkTab: () => null }));
vi.mock("./telegram-tab", () => ({ TelegramTab: () => null }));

const { IntegrationsTab } = await import("./integrations-tab");

describe("IntegrationsTab", () => {
  beforeEach(() => {
    queryCalls.length = 0;
    navigationRef.search = "";
    configRef.telegramBotsEnabled = false;
  });

  it("shows Telegram setup and probes configuration even before the server secret exists", () => {
    render(
      <I18nProvider locale="en" resources={{ en: { common: enCommon, settings: enSettings } }}>
        <IntegrationsTab />
      </I18nProvider>,
    );

    expect(screen.getByText(enSettings.integrations.telegram.name)).toBeInTheDocument();
    expect(queryCalls).toContainEqual(expect.objectContaining({
      queryKey: ["telegram", "workspace-1", "installations"],
      enabled: true,
    }));
  });

  it("opens the Figma connector when linked from a blocked design review", () => {
    navigationRef.search = "integration=figma";
    render(
      <I18nProvider locale="en" resources={{ en: { common: enCommon, settings: enSettings } }}>
        <IntegrationsTab />
      </I18nProvider>,
    );

    expect(screen.getByRole("button", { name: /Figma/ })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
  });
});
