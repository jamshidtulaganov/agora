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
// True when the effective list is the workspace's team sidebar (a member
// who never customized); the hook itself is covered in @agora/core.
const fromTeam = vi.hoisted(() => ({ current: false }));
const workspaceArg = vi.hoisted(() => ({ current: undefined as unknown }));
const WORKSPACE = vi.hoisted(() => ({ id: "ws-1", slug: "acme", settings: {} }));

vi.mock("@agora/core/sidebar", () => ({
  useEffectiveHiddenNav: (workspace: unknown) => {
    workspaceArg.current = workspace;
    return { hidden: hiddenNav.current, fromTeam: fromTeam.current };
  },
  useSetHiddenNav: () => ({ mutate: mockMutate }),
  toggleHiddenNavKey: (current: string[], key: string, hidden: boolean) => {
    const isHidden = current.includes(key);
    if (isHidden === hidden) return current;
    return hidden ? [...current, key] : current.filter((k) => k !== key);
  },
}));

vi.mock("@agora/core/paths", () => ({
  useCurrentWorkspace: () => WORKSPACE,
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
    fromTeam.current = false;
    workspaceArg.current = undefined;
  });

  it("reads the effective list for the current workspace", () => {
    renderSection();
    expect(workspaceArg.current).toBe(WORKSPACE);
    expect(screen.getByText(/This applies to you only/)).toBeInTheDocument();
  });

  // A member who never customized sees the team's sidebar. The switches show
  // exactly that, and an edit starts from it so the team's choices survive.
  describe("with the team sidebar in effect", () => {
    beforeEach(() => {
      hiddenNav.current = ["agents", "squads"];
      fromTeam.current = true;
    });

    it("says the admin picked these", () => {
      renderSection();
      expect(screen.getByText(/Your admin picked these for the team/)).toBeInTheDocument();
      expect(screen.getByRole("switch", { name: "Agents" })).not.toBeChecked();
    });

    it("hides one more item on top of the team's choices", async () => {
      const user = userEvent.setup();
      renderSection();
      await user.click(screen.getByRole("switch", { name: "Usage" }));
      expect(mockMutate).toHaveBeenCalledWith(["agents", "squads", "usage"], expect.anything());
    });

    it("restores a team-hidden item into the person's own list", async () => {
      const user = userEvent.setup();
      renderSection();
      await user.click(screen.getByRole("switch", { name: "Agents" }));
      expect(mockMutate).toHaveBeenCalledWith(["squads"], expect.anything());
    });
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
