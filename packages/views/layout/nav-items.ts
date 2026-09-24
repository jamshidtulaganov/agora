import {
  Inbox,
  ListTodo,
  Bot,
  Monitor,
  KeyRound,
  Settings,
  BookOpenText,
  CircleUser,
  FolderKanban,
  BarChart3,
  Zap,
  Users,
  Plug,
  Boxes,
  Workflow,
  Sparkles,
} from "lucide-react";

// Nav items reference WorkspacePaths method names so they can be resolved
// against the current workspace slug at render time (see AppSidebar body).
// Only parameterless paths are valid nav destinations.
export type NavKey =
  | "inbox"
  | "myIssues"
  | "assistant"
  | "policy"
  | "issues"
  | "projects"
  | "autopilots"
  | "automations"
  | "agents"
  | "squads"
  | "usage"
  | "runtimes"
  | "aiAccounts"
  | "skills"
  | "plugins"
  | "mcp"
  | "bitrix"
  | "settings";

// Static schema (key + icon) — labels resolved at render via useT("layout").
export type NavLabelKey =
  | "inbox"
  | "my_issues"
  | "assistant"
  | "policy"
  | "issues"
  | "projects"
  | "autopilots"
  | "automations"
  | "agents"
  | "squads"
  | "usage"
  | "runtimes"
  | "ai_accounts"
  | "skills"
  | "plugins"
  | "mcp"
  | "bitrix"
  | "settings";

export interface NavItem {
  key: NavKey;
  labelKey: NavLabelKey;
  icon: typeof Inbox;
}

export const personalNav: NavItem[] = [
  { key: "inbox", labelKey: "inbox", icon: Inbox },
  { key: "myIssues", labelKey: "my_issues", icon: CircleUser },
  { key: "assistant", labelKey: "assistant", icon: Sparkles },
  // Release remains reachable from issue review flows and direct URLs. It is
  // not a primary personal destination, so it does not occupy the sidebar.
  // "policy" (fleet cockpit) removed from the nav — the route stays reachable by
  // URL; agent fleet health/details will live inside the agent detail page.
];

export const workspaceNav: NavItem[] = [
  { key: "issues", labelKey: "issues", icon: ListTodo },
  { key: "projects", labelKey: "projects", icon: FolderKanban },
  { key: "autopilots", labelKey: "autopilots", icon: Zap },
  { key: "automations", labelKey: "automations", icon: Workflow },
  { key: "agents", labelKey: "agents", icon: Bot },
  { key: "squads", labelKey: "squads", icon: Users },
  { key: "usage", labelKey: "usage", icon: BarChart3 },
];

export const configureNav: NavItem[] = [
  { key: "runtimes", labelKey: "runtimes", icon: Monitor },
  { key: "aiAccounts", labelKey: "ai_accounts", icon: KeyRound },
  { key: "skills", labelKey: "skills", icon: BookOpenText },
  { key: "plugins", labelKey: "plugins", icon: Boxes },
  { key: "mcp", labelKey: "mcp", icon: Plug },
  // Bitrix removed from sidebar — accessed via Settings → Integrations instead.
  { key: "settings", labelKey: "settings", icon: Settings },
];

/** Group ids, used as i18n keys in the sidebar-customization UI. */
export type NavGroupId = "personal" | "workspace" | "configure";

export interface NavGroup {
  id: NavGroupId;
  items: NavItem[];
}

/**
 * The sidebar's three nav groups in render order. Consumed by the sidebar
 * itself and by the Settings → Preferences sidebar-customization list, so
 * both surfaces always agree on which items exist.
 */
export const NAV_GROUPS: NavGroup[] = [
  { id: "personal", items: personalNav },
  { id: "workspace", items: workspaceNav },
  { id: "configure", items: configureNav },
];

/**
 * Nav keys that can never be hidden. Settings is the only route back to the
 * screen where a hidden item is restored — hiding it would let a user lock
 * themselves out of their own preference. Mirrored server-side in
 * `alwaysVisibleNavKeys` (server/internal/handler/auth.go).
 */
export const ALWAYS_VISIBLE_NAV_KEYS: NavKey[] = ["settings"];

export function isNavKeyHideable(key: NavKey): boolean {
  return !ALWAYS_VISIBLE_NAV_KEYS.includes(key);
}

/**
 * Every workspace-scoped nav key this sidebar renders, in nav order.
 *
 * Exported so each app can assert its router actually serves them. The sidebar
 * is shared, so a key added here immediately becomes a clickable link in BOTH
 * web and desktop — but desktop's router is hand-maintained, and `ai-accounts`,
 * `plugins` and `mcp` all shipped as links that 404'd on desktop because
 * nobody added the matching route. See apps/desktop routes.test.tsx.
 *
 * Covers all three groups — `personalNav` entries (inbox / my-issues) are
 * workspace-scoped URLs too, not global ones.
 */
export const SIDEBAR_WORKSPACE_NAV_KEYS: NavKey[] = NAV_GROUPS.flatMap((group) =>
  group.items.map((item) => item.key),
);

/**
 * Filters a nav group down to what the sidebar should render for a user.
 * Always-visible keys survive even if they somehow appear in `hidden`, so a
 * stale or hand-crafted list can't strip the route back to Settings.
 */
export function visibleNavItems(
  items: NavItem[],
  hidden: readonly string[],
): NavItem[] {
  return items.filter(
    (item) => !isNavKeyHideable(item.key) || !hidden.includes(item.key),
  );
}
