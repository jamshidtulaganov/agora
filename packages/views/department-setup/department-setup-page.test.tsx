import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { setApiInstance } from "@agora/core/api";
import type { ApiClient } from "@agora/core/api";
import { I18nProvider } from "@agora/core/i18n/react";
import { WorkspaceSlugProvider } from "@agora/core/paths";
import type { AgentRuntime, AgentTemplateSummary, Workspace } from "@agora/core/types";
import { workspaceKeys } from "@agora/core/workspace/queries";
import { NavigationProvider, type NavigationAdapter } from "../navigation";
import { RESOURCES } from "../locales";
import { DepartmentSetupPage } from "./department-setup-page";
import { presetHiddenNav } from "./presets";

const toast = vi.hoisted(() =>
  Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn(), loading: vi.fn(() => "toast-1") }),
);
vi.mock("sonner", () => ({ toast }));

const authState = vi.hoisted(() => ({
  user: { id: "user-1", timezone: "Asia/Tashkent", hidden_nav: [] as string[] },
}));
vi.mock("@agora/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector?: (state: typeof authState) => unknown) => (selector ? selector(authState) : authState),
    { getState: () => authState },
  ),
}));

const WORKSPACE = {
  id: "ws-1",
  name: "Collections",
  slug: "acme",
  description: "",
  context: "We are the Collections team.",
  settings: {},
  repos: [],
  issue_prefix: "COL",
  avatar_url: null,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
} satisfies Workspace;

function template(slug: string, name: string, category = "Business"): AgentTemplateSummary {
  return { slug, name, description: `${name} does its job.`, category, icon: "ListChecks", accent: "info", skills: [] };
}

const TEMPLATES = [
  template("department-assistant", "Department Assistant"),
  template("intake-triager", "Intake Triager"),
  template("weekly-digest", "Weekly Digest"),
  template("code-reviewer", "Code Reviewer", "Engineering"),
];

function runtime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: null,
    name: "Office Mac",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    owner_id: "user-1",
    visibility: "private",
    last_seen_at: null,
    created_at: "",
    updated_at: "",
    ...overrides,
  } as AgentRuntime;
}

const api = {
  listWorkspaces: vi.fn(),
  listMembers: vi.fn(),
  listKnowledge: vi.fn(),
  listAgentTemplates: vi.fn(),
  listRuntimes: vi.fn(),
  listAgents: vi.fn(),
  updateTeamSidebar: vi.fn(),
  setDepartmentSetup: vi.fn(),
  createAgentFromTemplate: vi.fn(),
  createAutopilot: vi.fn(),
  createAutopilotTrigger: vi.fn(),
  updateWorkspace: vi.fn(),
  uploadFile: vi.fn(),
  createKnowledgeDoc: vi.fn(),
};

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  qc.setQueryData(workspaceKeys.list(), [WORKSPACE]);
  const nav: NavigationAdapter = {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/setup",
    searchParams: new URLSearchParams(),
    getShareableUrl: (p) => p,
  };
  const utils = render(
    <I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={qc}>
        <NavigationProvider value={nav}>
          <WorkspaceSlugProvider slug="acme">
            <DepartmentSetupPage />
          </WorkspaceSlugProvider>
        </NavigationProvider>
      </QueryClientProvider>
    </I18nProvider>,
  );
  return { ...utils, nav, qc };
}

beforeEach(() => {
  for (const fn of Object.values(api)) fn.mockReset();
  toast.error.mockClear();
  api.listWorkspaces.mockResolvedValue([WORKSPACE]);
  api.listMembers.mockResolvedValue([{ user_id: "user-1", role: "owner" }]);
  api.listKnowledge.mockResolvedValue({
    can_manage: true,
    documents: [
      {
        id: "doc-1",
        title: "Collections SOP",
        source: "upload",
        filename: "collections-sop.pdf",
        status: "ready",
        pinned: false,
        chunk_count: 4,
        created_at: "2026-09-25T10:00:00Z",
        updated_at: "2026-09-25T10:00:00Z",
      },
    ],
  });
  api.listAgentTemplates.mockResolvedValue(TEMPLATES);
  api.listRuntimes.mockResolvedValue([runtime({ id: "rt-offline", status: "offline" }), runtime()]);
  api.listAgents.mockResolvedValue([]);
  api.updateTeamSidebar.mockImplementation(async (_id: string, hidden: string[]) => ({
    ...WORKSPACE,
    settings: { team_sidebar: { hidden } },
  }));
  api.setDepartmentSetup.mockImplementation(async (_id: string, status: string) => ({
    ...WORKSPACE,
    settings: { department_setup: { status } },
  }));
  api.createAgentFromTemplate.mockImplementation(async ({ template_slug }: { template_slug: string }) => ({
    agent: { id: `agent-${template_slug}` },
    imported_skill_ids: [],
    reused_skill_ids: [],
  }));
  api.createAutopilot.mockResolvedValue({ id: "ap-1" });
  api.createAutopilotTrigger.mockResolvedValue({ id: "tr-1" });
  setApiInstance(api as unknown as ApiClient);
});

afterEach(() => cleanup());

async function goToAgentsStep(user: ReturnType<typeof userEvent.setup>) {
  await screen.findByTestId("setup-step-knowledge");
  await user.click(screen.getByTestId("setup-continue"));
  await screen.findByTestId("setup-step-sidebar");
  await user.click(screen.getByTestId("setup-continue"));
  return screen.findByTestId("setup-step-agents");
}

describe("DepartmentSetupPage — owners and admins", () => {
  it("walks knowledge → sidebar → agents → done", async () => {
    const user = userEvent.setup();
    renderPage();

    // Step 1 reuses the Knowledge pieces.
    const knowledge = await screen.findByTestId("setup-step-knowledge");
    expect(within(knowledge).getByRole("heading", { name: "Add what your team knows" })).toBeInTheDocument();
    expect(within(knowledge).getByText("Instructions for AI")).toBeInTheDocument();
    expect(await within(knowledge).findByText("Collections SOP")).toBeInTheDocument();
    await user.click(screen.getByTestId("setup-continue"));

    // Step 2 starts from Simple; adjusting one switch makes it custom.
    await screen.findByTestId("setup-step-sidebar");
    expect(screen.getByTestId("setup-preset-simple")).toHaveAttribute("data-selected", "true");
    expect(screen.getByRole("switch", { name: "Agents" })).not.toBeChecked();
    expect(screen.getByRole("switch", { name: "Issues" })).toBeChecked();
    await user.click(screen.getByRole("switch", { name: "Agents" }));
    expect(screen.getByTestId("setup-preset-simple")).toHaveAttribute("data-selected", "false");
    await user.click(screen.getByTestId("setup-continue"));

    await waitFor(() => expect(api.updateTeamSidebar).toHaveBeenCalledTimes(1));
    const [wsId, hidden] = api.updateTeamSidebar.mock.calls[0] as [string, string[]];
    expect(wsId).toBe("ws-1");
    expect(hidden).toEqual(presetHiddenNav("simple").filter((k) => k !== "agents"));

    // Step 3 offers the Business templates only.
    await screen.findByTestId("setup-step-agents");
    expect(await screen.findByTestId("setup-template-department-assistant")).toBeInTheDocument();
    expect(screen.getByTestId("setup-template-intake-triager")).toBeInTheDocument();
    expect(screen.queryByTestId("setup-template-code-reviewer")).toBeNull();
    await user.click(screen.getByTestId("setup-template-department-assistant"));
    await user.click(screen.getByTestId("setup-template-weekly-digest"));
    expect(screen.getByTestId("setup-template-weekly-digest")).toHaveAttribute("aria-pressed", "true");
    await user.click(screen.getByTestId("setup-finish"));

    await screen.findByTestId("setup-step-done");
    // Created on the online runtime, not the offline one listed first.
    expect(api.createAgentFromTemplate).toHaveBeenCalledTimes(2);
    expect(api.createAgentFromTemplate).toHaveBeenCalledWith({
      template_slug: "department-assistant",
      name: "Department Assistant",
      runtime_id: "rt-1",
    });
    expect(api.createAutopilot).toHaveBeenCalledWith(
      expect.objectContaining({
        assignee_type: "agent",
        assignee_id: "agent-weekly-digest",
        execution_mode: "create_issue",
        issue_title_template: "Weekly digest {{date}}",
      }),
    );
    expect(api.createAutopilotTrigger).toHaveBeenCalledWith("ap-1", {
      kind: "schedule",
      cron_expression: "0 9 * * 1",
      timezone: "Asia/Tashkent",
      label: "Every Monday at 9:00",
    });
    expect(api.setDepartmentSetup).toHaveBeenCalledWith("ws-1", "done");

    const recap = screen.getByTestId("setup-recap");
    expect(within(recap).getByText("Instructions for AI are in place")).toBeInTheDocument();
    expect(within(recap).getByText("1 document added")).toBeInTheDocument();
    expect(within(recap).getByText("Members see the sidebar you picked")).toBeInTheDocument();
    expect(within(recap).getByText("Department Assistant added")).toBeInTheDocument();
    expect(
      within(recap).getByText("Weekly Digest added. It runs every Monday at 9:00."),
    ).toBeInTheDocument();
  });

  it("shows a failed agent in the recap and still finishes", async () => {
    const user = userEvent.setup();
    api.createAgentFromTemplate.mockImplementation(async ({ template_slug }: { template_slug: string }) => {
      if (template_slug === "intake-triager") throw new Error("runtime is busy");
      return { agent: { id: `agent-${template_slug}` }, imported_skill_ids: [], reused_skill_ids: [] };
    });
    api.createAutopilotTrigger.mockRejectedValue(new Error("bad cron"));
    renderPage();
    await goToAgentsStep(user);
    await user.click(await screen.findByTestId("setup-template-intake-triager"));
    await user.click(screen.getByTestId("setup-template-weekly-digest"));
    await user.click(screen.getByTestId("setup-finish"));

    const recap = await screen.findByTestId("setup-recap");
    expect(within(recap).getByText("Couldn't add Intake Triager: runtime is busy")).toBeInTheDocument();
    expect(
      within(recap).getByText("Weekly Digest added, but its Monday schedule couldn't be set: bad cron"),
    ).toBeInTheDocument();
    expect(api.setDepartmentSetup).toHaveBeenCalledWith("ws-1", "done");
  });

  it("stays on the sidebar step when the save fails", async () => {
    const user = userEvent.setup();
    api.updateTeamSidebar.mockRejectedValue(new Error("forbidden"));
    renderPage();
    await screen.findByTestId("setup-step-knowledge");
    await user.click(screen.getByTestId("setup-continue"));
    await user.click(await screen.findByTestId("setup-preset-everything"));
    await user.click(screen.getByTestId("setup-continue"));

    await waitFor(() => expect(api.updateTeamSidebar).toHaveBeenCalledWith("ws-1", []));
    await waitFor(() => expect(toast.error).toHaveBeenCalled());
    expect(screen.getByTestId("setup-step-sidebar")).toBeInTheDocument();
  });

  it("takes the default setup from any step", async () => {
    const user = userEvent.setup();
    const { nav } = renderPage();
    await screen.findByTestId("setup-step-knowledge");
    await user.click(screen.getByTestId("setup-use-default"));

    await waitFor(() => expect(api.setDepartmentSetup).toHaveBeenCalledWith("ws-1", "skipped"));
    expect(nav.push).toHaveBeenCalledWith("/acme/issues");
    expect(api.updateTeamSidebar).not.toHaveBeenCalled();
  });

  it("points at Runtimes instead of offering agents when there's no runtime to use", async () => {
    const user = userEvent.setup();
    // Someone else's private runtime can't host new agents.
    api.listRuntimes.mockResolvedValue([runtime({ owner_id: "someone-else", visibility: "private" })]);
    renderPage();
    await goToAgentsStep(user);

    const notice = await screen.findByTestId("setup-no-runtime");
    expect(within(notice).getByRole("link", { name: "Set up a runtime" })).toHaveAttribute(
      "href",
      "/acme/runtimes",
    );
    expect(screen.queryByTestId("setup-template-department-assistant")).toBeNull();

    await user.click(screen.getByTestId("setup-finish"));
    await screen.findByTestId("setup-step-done");
    expect(api.createAgentFromTemplate).not.toHaveBeenCalled();
    expect(api.setDepartmentSetup).toHaveBeenCalledWith("ws-1", "done");
    expect(
      screen.getByText("No agents added. You can add them any time on the Agents page."),
    ).toBeInTheDocument();
  });

  it("marks templates whose agent already exists and doesn't create them again", async () => {
    const user = userEvent.setup();
    api.listAgents.mockResolvedValue([{ id: "a-1", name: "Department Assistant", archived_at: null }]);
    renderPage();
    await goToAgentsStep(user);

    const card = await screen.findByTestId("setup-template-department-assistant");
    await waitFor(() => expect(card).toBeDisabled());
    expect(within(card).getByText("Already added")).toBeInTheDocument();
  });
});

describe("DepartmentSetupPage — members", () => {
  it("tells a member only owners and admins can set this up", async () => {
    api.listMembers.mockResolvedValue([{ user_id: "user-1", role: "member" }]);
    api.listKnowledge.mockResolvedValue({ can_manage: false, documents: [] });
    renderPage();

    expect(await screen.findByTestId("setup-members-only")).toBeInTheDocument();
    expect(screen.getByText("Only owners and admins can set this up")).toBeInTheDocument();
    expect(screen.queryByTestId("setup-step-knowledge")).toBeNull();
  });
});
