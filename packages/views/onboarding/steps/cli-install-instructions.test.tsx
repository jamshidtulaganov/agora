import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enOnboarding from "../../locales/en/onboarding.json";
import { CliInstallInstructions } from "./cli-install-instructions";

const TEST_RESOURCES = { en: { common: enCommon, onboarding: enOnboarding } };

const ligatureClasses = [
  "[font-variant-ligatures:none]",
  "[font-feature-settings:'liga'_0]",
];

function withUserAgent(ua: string): () => void {
  const original = Object.getOwnPropertyDescriptor(window.navigator, "userAgent");
  Object.defineProperty(window.navigator, "userAgent", {
    value: ua,
    configurable: true,
  });
  return () => {
    if (original) {
      Object.defineProperty(window.navigator, "userAgent", original);
    } else {
      Reflect.deleteProperty(window.navigator, "userAgent");
    }
  };
}

function renderCard() {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <CliInstallInstructions />
    </I18nProvider>,
  );
}

function installCommandOrder(container: HTMLElement) {
  const codes = Array.from(container.querySelectorAll("code")).map(
    (node) => node.textContent ?? "",
  );
  return {
    unix: codes.findIndex((c) => c.includes("curl -fsSL")),
    windows: codes.findIndex((c) => c.includes("irm ")),
  };
}

describe("CliInstallInstructions", () => {
  it("shows install commands for both macOS/Linux and Windows", () => {
    renderCard();

    expect(
      screen.getByText(/^curl -fsSL .*install\.sh \| bash$/),
    ).toBeInTheDocument();
    expect(screen.getByText(/^irm .*install\.ps1 \| iex$/)).toBeInTheDocument();
  });

  it("leads with the macOS/Linux command by default", () => {
    const { container } = renderCard();

    const order = installCommandOrder(container);
    expect(order.unix).toBeGreaterThanOrEqual(0);
    expect(order.windows).toBeGreaterThanOrEqual(0);
    expect(order.unix).toBeLessThan(order.windows);
  });

  it("leads with the Windows command on a Windows device", () => {
    const restore = withUserAgent(
      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
    );
    try {
      const { container } = renderCard();

      const order = installCommandOrder(container);
      expect(order.windows).toBeGreaterThanOrEqual(0);
      expect(order.unix).toBeGreaterThanOrEqual(0);
      expect(order.windows).toBeLessThan(order.unix);
    } finally {
      restore();
    }
  });

  it("disables font ligatures in CLI command code", () => {
    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <CliInstallInstructions />
      </I18nProvider>,
    );

    // The CLI command renders as a single <code> node — the setup command is
    // the full `agora setup self-host …` string, so match it by prefix.
    expect(screen.getByText(/^agora setup self-host/)).toHaveClass(
      ...ligatureClasses,
    );
  });
});
