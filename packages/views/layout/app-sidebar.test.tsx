import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@agora/core/api";
import { AppSidebar } from "./app-sidebar";

const { detail, deletePin, pins, hiddenNav, setHiddenNav, toastFn } = vi.hoisted(() => ({
  detail: { current: { isPending: false, isError: false, data: null as unknown, error: null as unknown } },
  deletePin: vi.fn(),
  hiddenNav: { current: [] as string[] },
  setHiddenNav: vi.fn(),
  toastFn: vi.fn(),
  pins: {
    current: [
      {
        id: "pin-1",
        workspace_id: "ws-1",
        user_id: "user-1",
        item_type: "issue" as const,
        item_id: "issue-1",
        position: 0,
        created_at: "2026-05-06T00:00:00Z",
      },
    ],
  },
}));

vi.mock("@dnd-kit/core", () => ({
  DndContext: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  PointerSensor: vi.fn(),
  closestCenter: vi.fn(),
  useSensor: vi.fn(),
  useSensors: vi.fn(),
}));
vi.mock("@dnd-kit/sortable", () => ({
  SortableContext: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  useSortable: () => ({ attributes: {}, listeners: {}, setNodeRef: vi.fn() }),
  verticalListSortingStrategy: vi.fn(),
}));
vi.mock("@dnd-kit/utilities", () => ({ CSS: { Transform: { toString: () => undefined } } }));
vi.mock("@agora/ui/components/ui/sidebar", () => ({
  Sidebar: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarFooter: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarGroup: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarGroupContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarGroupLabel: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="sidebar-group-label">{children}</div>
  ),
  SidebarHeader: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarMenu: ({ children }: { children: React.ReactNode }) => <ul>{children}</ul>,
  // Surface the link target the real button forwards via `render`, so tests
  // can identify nav rows by destination (i18n isn't initialised here, so
  // every label renders empty).
  SidebarMenuButton: ({
    children,
    render,
  }: {
    children: React.ReactNode;
    render?: React.ReactElement<{ href?: string }>;
  }) => (
    <button type="button" data-href={render?.props?.href}>
      {children}
    </button>
  ),
  // A real <li>: NavRow hands this element to ContextMenuTrigger's `render`
  // prop, so it has to be a single host element the way the real component is.
  SidebarMenuItem: ({ children, ...props }: { children: React.ReactNode }) => (
    <li {...props}>{children}</li>
  ),
  SidebarRail: () => null,
}));
vi.mock("@agora/ui/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuContent: () => null,
  DropdownMenuGroup: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuItem: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuLabel: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuSeparator: () => null,
  DropdownMenuTrigger: ({ render }: { render: React.ReactNode }) => <>{render}</>,
}));
vi.mock("@agora/ui/components/ui/collapsible", () => ({
  Collapsible: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  CollapsibleContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  CollapsibleTrigger: () => <button type="button" />,
}));
vi.mock("@agora/ui/components/ui/tooltip", () => ({
  Tooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipTrigger: ({ children }: { children: React.ReactNode }) => <button type="button">{children}</button>,
}));
vi.mock("sonner", () => ({ toast: Object.assign(toastFn, { error: vi.fn() }) }));
vi.mock("./help-launcher", () => ({ HelpLauncher: () => null }));
vi.mock("../auth", () => ({ useLogout: () => vi.fn() }));
vi.mock("../issues/components/status-icon", () => ({ StatusIcon: () => <span /> }));
vi.mock("../navigation", () => ({
  AppLink: ({ children, href }: { children: React.ReactNode; href: string }) => <a href={href}>{children}</a>,
  useNavigation: () => ({ pathname: "/acme/issues", push: vi.fn() }),
}));
vi.mock("../projects/components/project-icon", () => ({ ProjectIcon: () => <span /> }));
vi.mock("../workspace/workspace-avatar", () => ({ WorkspaceAvatar: () => <span /> }));
vi.mock("@agora/ui/components/common/actor-avatar", () => ({ ActorAvatar: () => <span /> }));

vi.mock("@agora/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) => selector({ user: { id: "user-1" } }),
}));
vi.mock("@agora/core/paths", () => ({
  paths: { workspace: (slug: string) => ({ issues: () => `/${slug}/issues` }) },
  useCurrentWorkspace: () => ({ id: "ws-1", name: "Acme", slug: "acme" }),
  useWorkspacePaths: () => ({
    inbox: () => "/acme/inbox",
    myIssues: () => "/acme/my-issues",
    assistant: () => "/acme/assistant",
    qa: () => "/acme/qa",
    policy: () => "/acme/policy",
    issues: () => "/acme/issues",
    projects: () => "/acme/projects",
    knowledge: () => "/acme/knowledge",
    autopilots: () => "/acme/autopilots",
    automations: () => "/acme/automations",
    agents: () => "/acme/agents",
    squads: () => "/acme/squads",
    usage: () => "/acme/usage",
    runtimes: () => "/acme/runtimes",
    aiAccounts: () => "/acme/ai-accounts",
    skills: () => "/acme/skills",
    plugins: () => "/acme/plugins",
    mcp: () => "/acme/mcp",
    bitrix: () => "/acme/bitrix",
    settings: () => "/acme/settings",
    issueDetail: (id: string) => `/acme/issues/${id}`,
    projectDetail: (id: string) => `/acme/projects/${id}`,
  }),
}));
vi.mock("@agora/core/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@agora/core/api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      getBaseUrl: () => "http://127.0.0.1:8080",
    },
  };
});
vi.mock("@agora/core/inbox/queries", () => ({ deduplicateInboxItems: (items: unknown[]) => items, inboxKeys: { list: () => ["inbox"] } }));
vi.mock("@agora/core/inbox/mutations", () => ({
  useMarkAllInboxRead: () => ({ mutate: vi.fn(), isPending: false }),
}));
vi.mock("@agora/core/issues/queries", () => ({ issueDetailOptions: () => ({ queryKey: ["issue"] }) }));
vi.mock("@agora/core/issues/stores/create-mode-store", () => ({
  useCreateModeStore: { getState: () => ({ lastMode: "agent" }) },
  openCreateIssueWithPreference: vi.fn(),
}));
vi.mock("@agora/core/issues/stores/draft-store", () => ({ useIssueDraftStore: () => false }));
vi.mock("@agora/core/modals", () => ({ useModalStore: { getState: () => ({ modal: null, open: vi.fn() }) } }));
vi.mock("@agora/core/pins/mutations", () => ({ useDeletePin: () => ({ mutate: deletePin }), useReorderPins: () => ({ mutate: vi.fn() }) }));
vi.mock("@agora/core/pins/queries", () => ({ pinListOptions: () => ({ queryKey: ["pins"] }) }));
vi.mock("@agora/core/projects/queries", () => ({ projectDetailOptions: () => ({ queryKey: ["project"] }) }));
vi.mock("@agora/core/runtimes/hooks", () => ({ useMyRuntimesNeedUpdate: () => false }));
vi.mock("@agora/core/sidebar", () => ({
  useHiddenNav: () => hiddenNav.current,
  useSetHiddenNav: () => ({ mutate: setHiddenNav }),
  toggleHiddenNavKey: (current: string[], key: string, hidden: boolean) =>
    hidden ? [...current, key] : current.filter((k) => k !== key),
}));
vi.mock("@agora/core/workspace/queries", () => ({
  myInvitationListOptions: () => ({ queryKey: ["invitations"] }),
  workspaceKeys: { myInvitations: () => ["invitations"] },
  workspaceListOptions: () => ({ queryKey: ["workspaces"] }),
}));
// WorkspaceRunningIndicator (sidebar header) pulls these in; mock them like
// every other core dep so the sidebar renders headless.
vi.mock("@agora/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: () => "",
    getActorInitials: () => "",
    getActorAvatarUrl: () => null,
  }),
}));
vi.mock("@agora/core/agents", () => ({
  agentTaskSnapshotOptions: () => ({ queryKey: ["agent-task-snapshot"], queryFn: async () => [] }),
}));
vi.mock("@tanstack/react-query", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-query")>()),
  useMutation: () => ({ isPending: false, mutate: vi.fn() }),
  useQuery: ({ queryKey }: { queryKey: readonly unknown[] }) => {
    if (queryKey[0] === "pins") return { data: pins.current };
    if (queryKey[0] === "issue") return detail.current;
    return { data: [] };
  },
  useQueryClient: () => ({ fetchQuery: vi.fn(), invalidateQueries: vi.fn() }),
}));

describe("PinRow", () => {
  beforeEach(() => {
    deletePin.mockReset();
    detail.current = { isPending: false, isError: false, data: null, error: null };
  });

  it("unpins missing details", async () => {
    detail.current = { isPending: false, isError: true, data: null, error: new ApiError("missing", 404, "Not Found") };
    render(<AppSidebar />);
    await waitFor(() => expect(deletePin).toHaveBeenCalledTimes(1));
  });

  it("ignores non-404 errors", async () => {
    detail.current = { isPending: false, isError: true, data: null, error: new ApiError("error", 500, "Server Error") };
    render(<AppSidebar />);
    await waitFor(() => expect(deletePin).not.toHaveBeenCalled());
  });

  it("renders loaded details", async () => {
    detail.current = { isPending: false, isError: false, data: { identifier: "MUL-123", title: "Keep this pin", status: "todo" }, error: null };
    render(<AppSidebar />);
    expect(await screen.findByText("MUL-123 Keep this pin")).toBeInTheDocument();
  });

  it("does not render Release as a personal navigation item", () => {
    render(<AppSidebar />);
    expect(screen.queryByText("Release")).not.toBeInTheDocument();
  });
});

const WORKSPACE_NAV_KEYS = [
  "issues",
  "projects",
  "knowledge",
  "autopilots",
  "automations",
  "agents",
  "squads",
  "usage",
];

function navHrefs(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll("[data-href]")).map(
    (el) => el.getAttribute("data-href") ?? "",
  );
}

describe("per-user sidebar customization", () => {
  beforeEach(() => {
    hiddenNav.current = [];
    setHiddenNav.mockReset();
    toastFn.mockReset();
    detail.current = { isPending: false, isError: false, data: null, error: null };
  });

  it("renders every nav item when nothing is hidden", () => {
    const { container } = render(<AppSidebar />);
    const hrefs = navHrefs(container);
    expect(hrefs).toContain("/acme/usage");
    expect(hrefs).toContain("/acme/mcp");
    expect(hrefs).toContain("/acme/inbox");
  });

  it("omits the items the user hid", () => {
    hiddenNav.current = ["usage", "mcp"];
    const { container } = render(<AppSidebar />);
    const hrefs = navHrefs(container);
    expect(hrefs).not.toContain("/acme/usage");
    expect(hrefs).not.toContain("/acme/mcp");
    expect(hrefs).toContain("/acme/issues");
  });

  // Hiding Settings would strip the only route back to the screen that
  // restores hidden items, so the sidebar keeps it regardless.
  it("keeps Settings visible even if it appears in the hidden list", () => {
    hiddenNav.current = ["settings"];
    const { container } = render(<AppSidebar />);
    expect(navHrefs(container)).toContain("/acme/settings");
  });

  it("hides an item from its right-click menu and offers an undo", async () => {
    const user = userEvent.setup();
    const { container } = render(<AppSidebar />);
    const usageRow = container.querySelector("[data-href='/acme/usage']");
    expect(usageRow).not.toBeNull();

    await user.pointer({ keys: "[MouseRight]", target: usageRow as Element });
    // i18n isn't initialised here, so identify entries structurally: the hide
    // action is the plain menu item, "Customize sidebar" renders as a link.
    const items = await screen.findAllByRole("menuitem");
    expect(items).toHaveLength(1);
    expect(
      document.querySelector("a[href='/acme/settings?tab=preferences']"),
    ).not.toBeNull();
    await user.click(items[0] as HTMLElement);

    expect(setHiddenNav).toHaveBeenCalledWith(["usage"]);
    expect(toastFn).toHaveBeenCalled();

    // The toast's action restores the exact pre-hide list.
    const [, options] = toastFn.mock.calls[0] as [
      unknown,
      { action?: { onClick?: () => void } } | undefined,
    ];
    options?.action?.onClick?.();
    expect(setHiddenNav).toHaveBeenLastCalledWith([]);
  });

  // Settings must not offer a hide action at all.
  it("gives the always-visible item no context menu", async () => {
    const user = userEvent.setup();
    const { container } = render(<AppSidebar />);
    const settingsRow = container.querySelector("[data-href='/acme/settings']");
    await user.pointer({ keys: "[MouseRight]", target: settingsRow as Element });
    expect(screen.queryByRole("menuitem")).not.toBeInTheDocument();
  });

  it("drops the group heading once every item in the group is hidden", () => {
    const { container: full } = render(<AppSidebar />);
    const labelsBefore = full.querySelectorAll(
      "[data-testid='sidebar-group-label']",
    ).length;

    hiddenNav.current = [...WORKSPACE_NAV_KEYS];
    const { container: trimmed } = render(<AppSidebar />);
    const labelsAfter = trimmed.querySelectorAll(
      "[data-testid='sidebar-group-label']",
    ).length;

    expect(labelsAfter).toBe(labelsBefore - 1);
    // Configure is untouched, so its rows stay.
    expect(navHrefs(trimmed)).toContain("/acme/runtimes");
  });
});
