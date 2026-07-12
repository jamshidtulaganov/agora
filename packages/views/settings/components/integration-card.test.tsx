import { type ReactNode } from "react";
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";
import { IntegrationCard } from "./integration-card";

const TEST_RESOURCES = { en: { common: enCommon, settings: enSettings } };

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

const BODY = "connector configure form";

describe("IntegrationCard", () => {
  it("is collapsed by default: header shows, body hidden, aria-expanded=false", () => {
    render(
      <IntegrationCard icon={<span data-testid="icon" />} name="Figma" description="Read designs">
        <p>{BODY}</p>
      </IntegrationCard>,
      { wrapper: I18nWrapper },
    );
    expect(screen.getByText("Figma")).toBeTruthy();
    expect(screen.getByText("Read designs")).toBeTruthy();
    expect(screen.queryByText(BODY)).toBeNull();
    expect(screen.getByRole("button").getAttribute("aria-expanded")).toBe("false");
  });

  it("expands to reveal the body when the header button is clicked", async () => {
    render(
      <IntegrationCard icon={<span />} name="Figma" description="Read designs">
        <p>{BODY}</p>
      </IntegrationCard>,
      { wrapper: I18nWrapper },
    );
    await userEvent.click(screen.getByRole("button"));
    expect(screen.getByText(BODY)).toBeTruthy();
    expect(screen.getByRole("button").getAttribute("aria-expanded")).toBe("true");
  });

  it("starts expanded when defaultOpen is set", () => {
    render(
      <IntegrationCard icon={<span />} name="Figma" description="Read designs" defaultOpen>
        <p>{BODY}</p>
      </IntegrationCard>,
      { wrapper: I18nWrapper },
    );
    expect(screen.getByText(BODY)).toBeTruthy();
  });

  it("shows an emerald Connected badge for connected status", () => {
    render(
      <IntegrationCard icon={<span />} name="Figma" description="d" status="connected">
        <p>{BODY}</p>
      </IntegrationCard>,
      { wrapper: I18nWrapper },
    );
    expect(screen.getByText(enSettings.integrations.status_connected)).toBeTruthy();
    expect(screen.queryByText(enSettings.integrations.status_not_connected)).toBeNull();
  });

  it("shows a muted Not connected badge for not_connected status", () => {
    render(
      <IntegrationCard icon={<span />} name="Figma" description="d" status="not_connected">
        <p>{BODY}</p>
      </IntegrationCard>,
      { wrapper: I18nWrapper },
    );
    expect(screen.getByText(enSettings.integrations.status_not_connected)).toBeTruthy();
    expect(screen.queryByText(enSettings.integrations.status_connected)).toBeNull();
  });

  it("omits the status badge for launcher cards with no status", () => {
    render(
      <IntegrationCard icon={<span />} name="MCP servers" description="Launcher">
        <p>{BODY}</p>
      </IntegrationCard>,
      { wrapper: I18nWrapper },
    );
    expect(screen.queryByText(enSettings.integrations.status_connected)).toBeNull();
    expect(screen.queryByText(enSettings.integrations.status_not_connected)).toBeNull();
  });
});
