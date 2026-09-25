import { NAV_GROUPS, isNavKeyHideable, type NavKey } from "../layout/nav-items";

/** The two starting points for the team sidebar. */
export type SidebarPreset = "simple" | "everything";

export const SIDEBAR_PRESETS: readonly SidebarPreset[] = ["simple", "everything"];

/**
 * What "Simple" keeps: the pages a department does its daily work in.
 * Settings is always visible on top of these (ALWAYS_VISIBLE_NAV_KEYS).
 */
export const SIMPLE_VISIBLE_NAV: readonly NavKey[] = [
  "inbox",
  "myIssues",
  "assistant",
  "issues",
  "projects",
  "knowledge",
];

/**
 * "Simple" hides every other sidebar item, derived from the real nav so a
 * page added to the sidebar later starts out hidden in Simple rather than
 * silently appearing for a non-technical team.
 */
const SIMPLE_HIDDEN_NAV: string[] = NAV_GROUPS.flatMap((group) => group.items)
  .map((item) => item.key)
  .filter((key) => isNavKeyHideable(key) && !SIMPLE_VISIBLE_NAV.includes(key));

export function presetHiddenNav(preset: SidebarPreset): string[] {
  return preset === "simple" ? [...SIMPLE_HIDDEN_NAV] : [];
}

/** The preset a hidden list is exactly equal to, or null for a custom list. */
export function matchSidebarPreset(hidden: readonly string[]): SidebarPreset | null {
  const set = new Set(hidden.filter((key) => isNavKeyHideable(key as NavKey)));
  if (set.size === 0) return "everything";
  if (set.size === SIMPLE_HIDDEN_NAV.length && SIMPLE_HIDDEN_NAV.every((key) => set.has(key))) {
    return "simple";
  }
  return null;
}
