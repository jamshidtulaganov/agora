import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enProjects from "../../locales/en/projects.json";

const TEST_RESOURCES = { en: { common: enCommon, projects: enProjects } };

// Web has no OS folder picker and no "this machine" daemon, so attachment goes
// through the daemon-picker popover: the human names the host + types an
// absolute path, and the resource is created with no browser-side validation
// (the server + daemon own that). These tests pin that contract.

// Web shell: isDesktopShell=false, no local daemon. The desktop bridges exist
// as stubs but must never be called on web.
const platformMocks = vi.hoisted(() => ({
  pickDirectory: vi.fn(),
  validateLocalDirectory: vi.fn(),
  approveLocalDirectory: vi.fn(),
}));

vi.mock("../../platform", () => ({
  isDesktopShell: () => false,
  useLocalDaemonStatus: () => ({
    daemonId: null,
    deviceName: null,
    running: false,
  }),
  pickDirectory: platformMocks.pickDirectory,
  validateLocalDirectory: platformMocks.validateLocalDirectory,
  approveLocalDirectory: platformMocks.approveLocalDirectory,
}));

// Configurable runtime list so a test can drive the "no online daemon" branch.
const runtimeMocks = vi.hoisted(() => ({
  runtimes: [] as Array<Record<string, unknown>>,
}));

vi.mock("@agora/core/runtimes", () => ({
  runtimeListOptions: () => ({
    queryKey: ["runtimes", "ws-1", "list"],
    queryFn: () => Promise.resolve(runtimeMocks.runtimes),
  }),
}));

const mutateAsync = vi.hoisted(() => vi.fn());

vi.mock("@agora/core/projects", () => ({
  projectResourcesOptions: () => ({
    queryKey: ["project-resources", "test"],
    queryFn: () => Promise.resolve([]),
  }),
  useCreateProjectResource: () => ({
    mutateAsync,
    isPending: false,
  }),
  useUpdateProjectResource: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeleteProjectResource: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));

vi.mock("@agora/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@agora/core/paths", () => ({
  useCurrentWorkspace: () => ({ slug: "test-ws" }),
}));

// The folder picker walks the daemon's filesystem: the browse TARGET resolves
// which base to dial, and daemonListDir fetches one directory at a time.
const fsMocks = vi.hoisted(() => ({
  getDaemonBrowseTarget: vi.fn(),
  daemonListDir: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({
  api: { getDaemonBrowseTarget: fsMocks.getDaemonBrowseTarget },
}));

vi.mock("../../platform/daemon-fs-client", () => ({
  daemonListDir: fsMocks.daemonListDir,
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

import { ProjectResourcesSection } from "./project-resources-section";

function runtime(overrides: Record<string, unknown>) {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "d-1",
    name: "Dev Mac",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    owner_id: "u-1",
    visibility: "private",
    last_seen_at: null,
    created_at: "",
    updated_at: "",
    ...overrides,
  };
}

function renderSection() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <ProjectResourcesSection projectId="p-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("ProjectResourcesSection web local_directory attach", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mutateAsync.mockResolvedValue({ id: "r-new" });
    runtimeMocks.runtimes = [
      runtime({ id: "rt-1", daemon_id: "d-1", name: "Dev Mac" }),
    ];
    fsMocks.getDaemonBrowseTarget.mockResolvedValue({
      mode: "self-host",
      daemon_url: "http://127.0.0.1:19514",
    });
    // Home listing by default; drilling into /Users/dev/code returns its child.
    fsMocks.daemonListDir.mockImplementation((_base: string, path: string) => {
      if (path === "/Users/dev/code") {
        return Promise.resolve({
          path: "/Users/dev/code",
          parent: "/Users/dev",
          home: "/Users/dev",
          entries: [
            { name: "app", path: "/Users/dev/code/app", is_dir: true, is_git_repo: true, is_symlink: false },
          ],
          truncated: false,
        });
      }
      return Promise.resolve({
        path: "/Users/dev",
        parent: "", // home is a browsable root — no "up"
        home: "/Users/dev",
        entries: [
          { name: "code", path: "/Users/dev/code", is_dir: true, is_git_repo: false, is_symlink: false },
        ],
        truncated: false,
      });
    });
  });

  it("never exposes the desktop native picker on web", async () => {
    renderSection();
    expect(
      screen.queryByRole("button", { name: /add local directory/i }),
    ).toBeNull();
    // The web affordance is present instead.
    expect(
      await screen.findByRole("button", { name: /add local folder/i }),
    ).toBeTruthy();
  });

  it("creates a local_directory from host + path with no browser validation", async () => {
    const user = userEvent.setup();
    renderSection();
    await user.click(
      await screen.findByRole("button", { name: /add local folder/i }),
    );

    const path = await screen.findByPlaceholderText("/Users/you/Projects/app");
    await user.type(path, "/Users/dev/projects/app");
    await user.click(screen.getByRole("button", { name: /attach folder/i }));

    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    expect(mutateAsync).toHaveBeenCalledWith({
      resource_type: "local_directory",
      resource_ref: {
        local_path: "/Users/dev/projects/app",
        daemon_id: "d-1",
        access: "write",
      },
    });
    // No desktop bridge is ever touched on web.
    expect(platformMocks.pickDirectory).not.toHaveBeenCalled();
    expect(platformMocks.validateLocalDirectory).not.toHaveBeenCalled();
    expect(platformMocks.approveLocalDirectory).not.toHaveBeenCalled();
  });

  it("read-only access pins worktree isolation on the created ref", async () => {
    const user = userEvent.setup();
    renderSection();
    await user.click(
      await screen.findByRole("button", { name: /add local folder/i }),
    );

    await user.type(
      await screen.findByPlaceholderText("/Users/you/Projects/app"),
      "/srv/code",
    );
    await user.click(screen.getByRole("button", { name: /read-only/i }));
    await user.click(screen.getByRole("button", { name: /attach folder/i }));

    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    expect(mutateAsync.mock.calls[0]![0].resource_ref).toMatchObject({
      access: "read",
      isolation: "worktree",
    });
  });

  it("forwards an optional preview URL", async () => {
    const user = userEvent.setup();
    renderSection();
    await user.click(
      await screen.findByRole("button", { name: /add local folder/i }),
    );
    await user.type(
      await screen.findByPlaceholderText("/Users/you/Projects/app"),
      "/srv/code",
    );
    await user.type(
      screen.getByPlaceholderText("http://localhost:3000"),
      "http://localhost:5173",
    );
    await user.click(screen.getByRole("button", { name: /attach folder/i }));

    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    expect(mutateAsync.mock.calls[0]![0].resource_ref.preview_url).toBe(
      "http://localhost:5173",
    );
  });

  // The whole point of the picker: the human should never have to know or type
  // an absolute path on someone else's machine.
  it("browses the daemon's folders and fills the path from the picked folder", async () => {
    const user = userEvent.setup();
    renderSection();
    await user.click(
      await screen.findByRole("button", { name: /add local folder/i }),
    );
    await user.click(screen.getByRole("button", { name: /^browse$/i }));

    // Landing view is the daemon's home — asked for with an empty path.
    await waitFor(() =>
      expect(fsMocks.daemonListDir).toHaveBeenCalledWith("http://127.0.0.1:19514", ""),
    );
    expect(await screen.findByRole("button", { name: /code/i })).toBeTruthy();

    // Drill into a folder, then commit it.
    await user.click(screen.getByRole("button", { name: /code/i }));
    await waitFor(() =>
      expect(fsMocks.daemonListDir).toHaveBeenCalledWith(
        "http://127.0.0.1:19514",
        "/Users/dev/code",
      ),
    );
    await user.click(screen.getByRole("button", { name: /use this folder/i }));

    // The path input is the source of truth and now carries the picked folder.
    const path = await screen.findByLabelText("Absolute path");
    await waitFor(() => expect((path as HTMLInputElement).value).toBe("/Users/dev/code"));

    // ...and attaching sends exactly that, with no browser-side validation.
    await user.click(screen.getByRole("button", { name: /attach folder/i }));
    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    expect(mutateAsync.mock.calls[0]![0].resource_ref).toMatchObject({
      local_path: "/Users/dev/code",
      daemon_id: "d-1",
    });
  });

  it("tells the user to type a path when the machine is offline instead of spinning", async () => {
    fsMocks.getDaemonBrowseTarget.mockResolvedValue({ mode: "offline", daemon_url: "" });
    const user = userEvent.setup();
    renderSection();
    await user.click(
      await screen.findByRole("button", { name: /add local folder/i }),
    );
    await user.click(screen.getByRole("button", { name: /^browse$/i }));

    expect(await screen.findByText(/machine is offline/i)).toBeTruthy();
    expect(fsMocks.daemonListDir).not.toHaveBeenCalled();
  });

  it("surfaces a daemon listing error in place without closing the picker", async () => {
    fsMocks.daemonListDir.mockRejectedValue(
      new Error("path is outside the browsable roots"),
    );
    const user = userEvent.setup();
    renderSection();
    await user.click(
      await screen.findByRole("button", { name: /add local folder/i }),
    );
    await user.click(screen.getByRole("button", { name: /^browse$/i }));

    expect(
      await screen.findByText(/outside the browsable roots/i),
    ).toBeTruthy();
    // The picker stays up so the user can navigate elsewhere.
    expect(screen.getByRole("button", { name: /use this folder/i })).toBeTruthy();
  });

  it("shows a guidance message and no path field when no daemon is online", async () => {
    runtimeMocks.runtimes = [
      runtime({ id: "rt-off", daemon_id: "d-off", status: "offline" }),
    ];
    const user = userEvent.setup();
    renderSection();
    await user.click(
      await screen.findByRole("button", { name: /add local folder/i }),
    );

    expect(await screen.findByText(/no online runtime/i)).toBeTruthy();
    expect(
      screen.queryByPlaceholderText("/Users/you/Projects/app"),
    ).toBeNull();
  });
});
