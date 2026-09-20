import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const configRef = vi.hoisted(() => ({
  bitrixEnabled: false,
  zohoEnabled: false,
  larkEnabled: false,
  slackEnabled: false,
  telegramBotsEnabled: false,
}));
const queryCalls = vi.hoisted(() => [] as { queryKey?: unknown; enabled?: boolean }[]);
const navigationRef = vi.hoisted(() => ({ search: "" }));
// The assistant hint is gated on the SAME availability query the FAB uses, so
// the mock has to be able to answer both ways within one file.
const assistantRef = vi.hoisted(() => ({
  availability: undefined as { enabled: boolean } | undefined,
  setOpen: vi.fn(),
}));

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
    const key = Array.isArray(options.queryKey) ? options.queryKey[0] : undefined;
    if (key === "assistant") return { data: assistantRef.availability };
    return { data: undefined };
  },
  queryOptions: <T,>(options: T) => options,
}));
vi.mock("@agora/core/assistant", () => ({
  assistantAvailabilityOptions: () => ({ queryKey: ["assistant", "availability"] }),
  useAssistantPanelStore: (selector: (state: { setOpen: (open: boolean) => void }) => unknown) =>
    selector({ setOpen: assistantRef.setOpen }),
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
vi.mock("@agora/core/imports", () => ({
  importConnectionsOptions: () => ({ queryKey: ["imports", "connections", "workspace-1"] }),
}));
vi.mock("@agora/core/lark", () => ({
  larkInstallationsOptions: () => ({ queryKey: ["lark"] }),
}));
vi.mock("@agora/core/telegram", () => ({
  telegramInstallationsOptions: () => ({ queryKey: ["telegram", "workspace-1", "installations"] }),
}));
vi.mock("@agora/core/slack", () => ({
  slackInstallationsOptions: () => ({ queryKey: ["slack", "workspace-1", "installations"] }),
}));
vi.mock("./mcp-servers-tab", () => ({ McpServersTab: () => null }));
vi.mock("./release-integrations-section", () => ({ ReleaseIntegrationsSection: () => null }));
vi.mock("./figma-integration-section", () => ({ FigmaIntegrationSection: () => null }));
vi.mock("./bitrix-tab", () => ({ BitrixTab: () => null }));
vi.mock("./zoho-tab", () => ({ ZohoTab: () => null }));
vi.mock("./import-section", () => ({ ImportSection: () => null }));
vi.mock("./lark-tab", () => ({ LarkTab: () => null }));
vi.mock("./telegram-tab", () => ({ TelegramTab: () => null }));
vi.mock("./slack-tab", () => ({ SlackTab: () => null }));

const { IntegrationsTab } = await import("./integrations-tab");

describe("IntegrationsTab", () => {
  beforeEach(() => {
    queryCalls.length = 0;
    navigationRef.search = "";
    configRef.telegramBotsEnabled = false;
    configRef.slackEnabled = false;
    assistantRef.availability = undefined;
    assistantRef.setOpen.mockClear();
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

  // The Slack APP card is gated on the four-key server gate. A deployment
  // without a Slack app must not show a Connect button that dies at the OAuth
  // exchange — and must not fetch its installations either.
  it("hides Slack on a deployment with no Slack app configured", () => {
    render(
      <I18nProvider locale="en" resources={{ en: { common: enCommon, settings: enSettings } }}>
        <IntegrationsTab />
      </I18nProvider>,
    );

    expect(screen.queryByText(enSettings.integrations.slack.name)).toBeNull();
    expect(queryCalls).toContainEqual(expect.objectContaining({
      queryKey: ["slack", "workspace-1", "installations"],
      enabled: false,
    }));
  });

  it("shows Slack once the deployment reports the app as configured", () => {
    configRef.slackEnabled = true;
    render(
      <I18nProvider locale="en" resources={{ en: { common: enCommon, settings: enSettings } }}>
        <IntegrationsTab />
      </I18nProvider>,
    );

    expect(screen.getByText(enSettings.integrations.slack.name)).toBeInTheDocument();
    expect(queryCalls).toContainEqual(expect.objectContaining({
      queryKey: ["slack", "workspace-1", "installations"],
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

  // The hint is the discoverability half of list_integrations: the assistant
  // can read this roster and walk someone through a connector, and nothing
  // else on the page says so.
  it("offers the assistant walkthrough when the assistant is available", async () => {
    assistantRef.availability = { enabled: true };
    render(
      <I18nProvider locale="en" resources={{ en: { common: enCommon, settings: enSettings } }}>
        <IntegrationsTab />
      </I18nProvider>,
    );

    const user = userEvent.setup();
    const hint = screen.getByRole("button", {
      name: enSettings.integrations.assistant_hint,
    });
    await user.click(hint);
    expect(assistantRef.setOpen).toHaveBeenCalledWith(true);
  });

  // An instance with the assistant switched off must not advertise it —
  // the same gate the FAB applies.
  it("hides the hint when the instance has no assistant", () => {
    assistantRef.availability = { enabled: false };
    render(
      <I18nProvider locale="en" resources={{ en: { common: enCommon, settings: enSettings } }}>
        <IntegrationsTab />
      </I18nProvider>,
    );

    expect(
      screen.queryByText(enSettings.integrations.assistant_hint),
    ).not.toBeInTheDocument();
  });
});
