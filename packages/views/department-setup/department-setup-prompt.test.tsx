import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { setApiInstance } from "@agora/core/api";
import type { ApiClient } from "@agora/core/api";
import { I18nProvider } from "@agora/core/i18n/react";
import { useProductTourStore, useWelcomeStore } from "@agora/core/onboarding";
import { WorkspaceSlugProvider } from "@agora/core/paths";
import type { Workspace } from "@agora/core/types";
import { workspaceKeys } from "@agora/core/workspace/queries";
import { useSetupPromptStore } from "@agora/core/workspace/setup-prompt-store";
import { NavigationProvider, type NavigationAdapter } from "../navigation";
import { RESOURCES } from "../locales";
import { DepartmentSetupPrompt } from "./department-setup-prompt";

const toast = vi.hoisted(() => Object.assign(vi.fn(), { error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

const authState = vi.hoisted(() => ({ user: { id: "user-1" } }));
vi.mock("@agora/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector?: (state: typeof authState) => unknown) => (selector ? selector(authState) : authState),
    { getState: () => authState },
  ),
}));

function workspace(settings: Record<string, unknown> = {}): Workspace {
  return {
    id: "ws-1",
    name: "Collections",
    slug: "acme",
    description: null,
    context: null,
    settings,
    repos: [],
    issue_prefix: "COL",
    avatar_url: null,
    created_at: "",
    updated_at: "",
  };
}

const api = {
  listWorkspaces: vi.fn(),
  listMembers: vi.fn(),
  setDepartmentSetup: vi.fn(),
};

function renderPrompt({
  ws = workspace(),
  role = "owner",
  pathname = "/acme/issues",
}: { ws?: Workspace; role?: string; pathname?: string } = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  qc.setQueryData(workspaceKeys.list(), [ws]);
  qc.setQueryData(workspaceKeys.members("ws-1"), [{ user_id: "user-1", role }]);
  // A tiny stateful server: the settle refetch returns what was last saved.
  let current = ws;
  api.listWorkspaces.mockImplementation(async () => [current]);
  api.setDepartmentSetup.mockImplementation(async (_id: string, status: string) => {
    current = workspace({ ...current.settings, department_setup: { status } });
    return current;
  });
  const nav: NavigationAdapter = {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname,
    searchParams: new URLSearchParams(),
    getShareableUrl: (p) => p,
  };
  const utils = render(
    <I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={qc}>
        <NavigationProvider value={nav}>
          <WorkspaceSlugProvider slug="acme">
            <DepartmentSetupPrompt />
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
  setApiInstance(api as unknown as ApiClient);
  useSetupPromptStore.setState({ dismissed: {} });
  useProductTourStore.setState({ workspaceId: null });
  useWelcomeStore.getState().reset();
});

afterEach(() => cleanup());

describe("DepartmentSetupPrompt visibility", () => {
  it.each(["owner", "admin"])("shows to an %s while nobody has decided", (role) => {
    renderPrompt({ role });
    expect(screen.getByTestId("setup-prompt")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Set up Agora for your team" })).toBeInTheDocument();
  });

  it("stays hidden for members", () => {
    renderPrompt({ role: "member" });
    expect(screen.queryByTestId("setup-prompt")).toBeNull();
  });

  it.each(["done", "skipped"])("stays hidden once the setup is %s", (status) => {
    renderPrompt({ ws: workspace({ department_setup: { status } }) });
    expect(screen.queryByTestId("setup-prompt")).toBeNull();
  });

  it("still shows when the stored status is unreadable", () => {
    renderPrompt({ ws: workspace({ department_setup: { status: "later" } }) });
    expect(screen.getByTestId("setup-prompt")).toBeInTheDocument();
  });

  it("waits for the product tour", () => {
    useProductTourStore.setState({ workspaceId: "ws-1" });
    renderPrompt();
    expect(screen.queryByTestId("setup-prompt")).toBeNull();

    act(() => useProductTourStore.getState().stop());
    expect(screen.getByTestId("setup-prompt")).toBeInTheDocument();
  });

  it("waits for the welcome after onboarding", () => {
    useWelcomeStore.getState().set({ workspaceId: "ws-1", choice: "skip" });
    renderPrompt();
    expect(screen.queryByTestId("setup-prompt")).toBeNull();

    act(() => useWelcomeStore.getState().dismiss());
    expect(screen.getByTestId("setup-prompt")).toBeInTheDocument();
  });

  it("doesn't show on the setup page itself", () => {
    renderPrompt({ pathname: "/acme/setup" });
    expect(screen.queryByTestId("setup-prompt")).toBeNull();
  });
});

describe("DepartmentSetupPrompt actions", () => {
  it("opens the setup", async () => {
    const user = userEvent.setup();
    const { nav } = renderPrompt();
    await user.click(screen.getByTestId("setup-prompt-start"));
    expect(nav.push).toHaveBeenCalledWith("/acme/setup");
  });

  it("records the default setup and goes away", async () => {
    const user = userEvent.setup();
    renderPrompt();
    await user.click(screen.getByTestId("setup-prompt-default"));
    expect(api.setDepartmentSetup).toHaveBeenCalledWith("ws-1", "skipped");
    await waitFor(() => expect(screen.queryByTestId("setup-prompt")).toBeNull());
  });

  it("comes back with a toast when recording the default fails", async () => {
    const user = userEvent.setup();
    renderPrompt();
    api.setDepartmentSetup.mockRejectedValue(new Error("offline"));
    await user.click(screen.getByTestId("setup-prompt-default"));
    await waitFor(() => expect(toast.error).toHaveBeenCalled());
    expect(await screen.findByTestId("setup-prompt")).toBeInTheDocument();
  });

  it("closes for this session without recording anything", async () => {
    const user = userEvent.setup();
    renderPrompt();
    await user.click(screen.getByRole("button", { name: "Not now" }));
    expect(screen.queryByTestId("setup-prompt")).toBeNull();
    expect(api.setDepartmentSetup).not.toHaveBeenCalled();
  });
});
