import { describe, expect, it } from "vitest";
import {
  ALWAYS_VISIBLE_NAV_KEYS,
  NAV_GROUPS,
  SIDEBAR_WORKSPACE_NAV_KEYS,
  configureNav,
  isNavKeyHideable,
  visibleNavItems,
  workspaceNav,
} from "./nav-items";

describe("visibleNavItems", () => {
  it("drops hidden keys", () => {
    const visible = visibleNavItems(workspaceNav, ["usage", "squads"]);
    expect(visible.map((i) => i.key)).not.toContain("usage");
    expect(visible.map((i) => i.key)).not.toContain("squads");
    expect(visible.map((i) => i.key)).toContain("issues");
  });

  it("returns every item when nothing is hidden", () => {
    expect(visibleNavItems(workspaceNav, [])).toHaveLength(workspaceNav.length);
  });

  // Settings is the only route back to the screen that restores a hidden
  // item, so a hand-crafted or stale list must not be able to remove it.
  it("keeps always-visible items even when listed as hidden", () => {
    const visible = visibleNavItems(configureNav, ["settings", "mcp"]);
    expect(visible.map((i) => i.key)).toContain("settings");
    expect(visible.map((i) => i.key)).not.toContain("mcp");
  });

  it("ignores unknown keys", () => {
    expect(visibleNavItems(workspaceNav, ["not-a-nav-key"])).toHaveLength(
      workspaceNav.length,
    );
  });
});

describe("isNavKeyHideable", () => {
  it("rejects the always-visible keys", () => {
    for (const key of ALWAYS_VISIBLE_NAV_KEYS) {
      expect(isNavKeyHideable(key)).toBe(false);
    }
  });

  it("accepts a normal nav key", () => {
    expect(isNavKeyHideable("usage")).toBe(true);
  });
});

describe("SIDEBAR_WORKSPACE_NAV_KEYS", () => {
  it("covers every group with no duplicates", () => {
    const fromGroups = NAV_GROUPS.flatMap((g) => g.items.map((i) => i.key));
    expect(SIDEBAR_WORKSPACE_NAV_KEYS).toEqual(fromGroups);
    expect(new Set(SIDEBAR_WORKSPACE_NAV_KEYS).size).toBe(
      SIDEBAR_WORKSPACE_NAV_KEYS.length,
    );
  });
});
