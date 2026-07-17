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

describe("CliInstallInstructions", () => {
  it("shows install commands for both macOS/Linux and Windows", () => {
    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <CliInstallInstructions />
      </I18nProvider>,
    );

    expect(
      screen.getByText(/^curl -fsSL .*install\.sh \| bash$/),
    ).toBeInTheDocument();
    expect(screen.getByText(/^irm .*install\.ps1 \| iex$/)).toBeInTheDocument();
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
