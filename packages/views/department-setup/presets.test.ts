import { describe, expect, it } from "vitest";
import { matchSidebarPreset, presetHiddenNav } from "./presets";

describe("sidebar presets", () => {
  it("Simple hides everything but the daily-work pages", () => {
    expect(new Set(presetHiddenNav("simple"))).toEqual(
      new Set([
        "autopilots",
        "automations",
        "agents",
        "squads",
        "usage",
        "runtimes",
        "aiAccounts",
        "skills",
        "plugins",
        "mcp",
      ]),
    );
  });

  it("never hides Settings", () => {
    expect(presetHiddenNav("simple")).not.toContain("settings");
  });

  it("Everything hides nothing", () => {
    expect(presetHiddenNav("everything")).toEqual([]);
  });

  it("returns a fresh copy each time", () => {
    const a = presetHiddenNav("simple");
    a.push("inbox");
    expect(presetHiddenNav("simple")).not.toContain("inbox");
  });

  it("recognizes a preset regardless of order", () => {
    expect(matchSidebarPreset([...presetHiddenNav("simple")].reverse())).toBe("simple");
    expect(matchSidebarPreset([])).toBe("everything");
  });

  it("treats a stray settings key as nothing hidden", () => {
    expect(matchSidebarPreset(["settings"])).toBe("everything");
  });

  it("reads an adjusted list as custom", () => {
    expect(matchSidebarPreset(presetHiddenNav("simple").slice(1))).toBeNull();
    expect(matchSidebarPreset(["usage"])).toBeNull();
  });
});
