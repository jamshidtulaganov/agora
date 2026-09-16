import type { ReactNode } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enLayout from "../../locales/en/layout.json";
import enSettings from "../../locales/en/settings.json";

const mockMutate = vi.hoisted(() => vi.fn());
const hiddenNav = vi.hoisted(() => ({ current: [] as string[] }));

vi.mock("@agora/core/sidebar", () => ({
  useHiddenNav: () => hiddenNav.current,
  useSetHiddenNav: () => ({ mutate: mockMutate }),
  toggleHiddenNavKey: (current: string[], key: string, hidden: boolean) => {
    const isHidden = current.includes(key);
    if (isHidden === hidden) return current;
    return hidden ? [...current, key] : current.filter((k) => k !== key);
  },
}));

vi.mock("sonner", () => ({ toast: { error: vi.fn() } }));

import { SidebarSection } from "./sidebar-section";

const TEST_RESOURCES = {
  en: { common: enCommon, layout: enLayout, settings: enSettings },
};

function renderSection() {
  const wrapper = ({ children }: { children: ReactNode }) => (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
  return render(<SidebarSection />, { wrapper });
}

describe("SidebarSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    hiddenNav.current = [];
  });

  it("lists every nav item with its real label", () => {
    renderSection();
    expect(screen.getByText("Inbox")).toBeInTheDocument();
    expect(screen.getByText("Usage")).toBeInTheDocument();
    expect(screen.getByText("MCP servers")).toBeInTheDocument();
  });

  it("shows a nav item as on when it isn't hidden", () => {
    renderSection();
    expect(screen.getByRole("switch", { name: "Usage" })).toBeChecked();
  });

  it("shows a nav item as off when it is hidden", () => {
    hiddenNav.current = ["usage"];
    renderSection();
    expect(screen.getByRole("switch", { name: "Usage" })).not.toBeChecked();
  });

  it("hides an item by appending its key", async () => {
    const user = userEvent.setup();
    renderSection();
    await user.click(screen.getByRole("switch", { name: "Usage" }));
    expect(mockMutate).toHaveBeenCalledWith(["usage"], expect.anything());
  });

  it("restores an item by removing its key and leaves the rest hidden", async () => {
    hiddenNav.current = ["usage", "mcp"];
    const user = userEvent.setup();
    renderSection();
    await user.click(screen.getByRole("switch", { name: "Usage" }));
    expect(mockMutate).toHaveBeenCalledWith(["mcp"], expect.anything());
  });

  // Settings is the way back to this very screen, so it gets a static label
  // instead of a switch.
  it("renders Settings as always visible with no switch", () => {
    renderSection();
    expect(
      screen.queryByRole("switch", { name: "Settings" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText("Always visible")).toBeInTheDocument();
  });

  it("does not offer Show all when nothing is hidden", () => {
    renderSection();
    expect(
      screen.queryByRole("button", { name: "Show all" }),
    ).not.toBeInTheDocument();
  });

  it("resets the whole list from Show all", async () => {
    hiddenNav.current = ["usage", "mcp"];
    const user = userEvent.setup();
    renderSection();
    await user.click(screen.getByRole("button", { name: "Show all" }));
    expect(mockMutate).toHaveBeenCalledWith([], expect.anything());
  });
});
