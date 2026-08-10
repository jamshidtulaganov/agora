import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enProjects from "../../locales/en/projects.json";

const TEST_RESOURCES = { en: { common: enCommon, projects: enProjects } };

// The attach flow's consent ordering is the subject under test: the desktop
// approve bridge must be called AFTER validation succeeds and BEFORE the
// resource create fires — and an approve failure must abort the create
// (attaching a folder the daemon will refuse only manufactures failed tasks).
const platformMocks = vi.hoisted(() => ({
  pickDirectory: vi.fn(),
  validateLocalDirectory: vi.fn(),
  approveLocalDirectory: vi.fn(),
  callOrder: [] as string[],
  resources: [] as Array<Record<string, unknown>>,
}));

vi.mock("../../platform", () => ({
  isDesktopShell: () => true,
  useLocalDaemonStatus: () => ({
    daemonId: "d-local",
    deviceName: "Test Mac",
    running: true,
  }),
  pickDirectory: (...args: unknown[]) => {
    platformMocks.callOrder.push("pick");
    return platformMocks.pickDirectory(...args);
  },
  validateLocalDirectory: (...args: unknown[]) => {
    platformMocks.callOrder.push("validate");
    return platformMocks.validateLocalDirectory(...args);
  },
  approveLocalDirectory: (...args: unknown[]) => {
    platformMocks.callOrder.push("approve");
    return platformMocks.approveLocalDirectory(...args);
  },
}));

const mutateAsync = vi.hoisted(() => vi.fn());
const updateMutateAsync = vi.hoisted(() => vi.fn());

vi.mock("@agora/core/projects", () => ({
  projectResourcesOptions: () => ({
    queryKey: ["project-resources", "test"],
    queryFn: () => Promise.resolve(platformMocks.resources),
  }),
  useCreateProjectResource: () => ({
    mutateAsync: (...args: unknown[]) => {
      platformMocks.callOrder.push("create");
      return mutateAsync(...args);
    },
    isPending: false,
  }),
  useUpdateProjectResource: () => ({ mutateAsync: updateMutateAsync, isPending: false }),
  useDeleteProjectResource: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));

vi.mock("@agora/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@agora/core/paths", () => ({
  useCurrentWorkspace: () => ({ slug: "test-ws" }),
}));

vi.mock("@agora/core/api", () => ({
  api: {},
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

import { ProjectResourcesSection } from "./project-resources-section";

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

async function runAttachFlow(user: ReturnType<typeof userEvent.setup>) {
  // The local-directory attach button renders standalone (desktop mode only),
  // outside the git-repo add popover.
  await user.click(
    await screen.findByRole("button", { name: /add local directory/i }),
  );
}

describe("ProjectResourcesSection local_directory attach flow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    platformMocks.callOrder.length = 0;
    platformMocks.resources = [];
    platformMocks.pickDirectory.mockResolvedValue({
      ok: true,
      path: "/Users/dev/projects/app",
      basename: "app",
    });
    platformMocks.validateLocalDirectory.mockResolvedValue({ ok: true });
    platformMocks.approveLocalDirectory.mockResolvedValue({ ok: true });
    mutateAsync.mockResolvedValue({ id: "r-new" });
    updateMutateAsync.mockResolvedValue({ id: "r-updated" });
  });

  it("approves after validation and before the resource create", async () => {
    const user = userEvent.setup();
    renderSection();
    await runAttachFlow(user);

    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    expect(platformMocks.approveLocalDirectory).toHaveBeenCalledWith(
      "/Users/dev/projects/app",
    );
    expect(platformMocks.callOrder).toEqual([
      "pick",
      "validate",
      "approve",
      "create",
    ]);
  });

  it("aborts the create when approval fails", async () => {
    platformMocks.approveLocalDirectory.mockResolvedValue({
      ok: false,
      reason: "protected",
    });
    const user = userEvent.setup();
    renderSection();
    await runAttachFlow(user);

    await waitFor(() =>
      expect(platformMocks.approveLocalDirectory).toHaveBeenCalledTimes(1),
    );
    expect(mutateAsync).not.toHaveBeenCalled();
  });

  it("proceeds when the shell has no approve bridge (older desktop)", async () => {
    platformMocks.approveLocalDirectory.mockResolvedValue({
      ok: false,
      reason: "unsupported",
    });
    const user = userEvent.setup();
    renderSection();
    await runAttachFlow(user);

    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
  });

  it("allows an additional local folder and inherits the primary worktree mode", async () => {
    platformMocks.resources = [{
      id: "r-primary",
      project_id: "p-1",
      workspace_id: "ws-1",
      resource_type: "local_directory",
      resource_ref: {
        local_path: "/Users/dev/projects/api",
        daemon_id: "d-local",
        isolation: "worktree",
        access: "write",
      },
      label: null,
      position: 0,
      created_at: "2026-08-11T00:00:00Z",
      created_by: "u-1",
    }];
    platformMocks.pickDirectory.mockResolvedValue({
      ok: true,
      path: "/Users/dev/projects/web",
      basename: "web",
    });

    const user = userEvent.setup();
    renderSection();
    await runAttachFlow(user);

    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1));
    expect(mutateAsync).toHaveBeenCalledWith({
      resource_type: "local_directory",
      resource_ref: {
        local_path: "/Users/dev/projects/web",
        daemon_id: "d-local",
        label: "web",
        isolation: "worktree",
        access: "write",
      },
    });
  });

  it("promotes an additional folder to the primary working directory", async () => {
    platformMocks.resources = [
      {
        id: "r-primary",
        project_id: "p-1",
        workspace_id: "ws-1",
        resource_type: "local_directory",
        resource_ref: {
          local_path: "/Users/dev/projects/api",
          daemon_id: "d-local",
          access: "write",
        },
        label: null,
        position: 2,
        created_at: "2026-08-11T00:00:00Z",
        created_by: "u-1",
      },
      {
        id: "r-web",
        project_id: "p-1",
        workspace_id: "ws-1",
        resource_type: "local_directory",
        resource_ref: {
          local_path: "/Users/dev/projects/web",
          daemon_id: "d-local",
          access: "write",
        },
        label: null,
        position: 3,
        created_at: "2026-08-11T00:00:00Z",
        created_by: "u-1",
      },
    ];

    const user = userEvent.setup();
    renderSection();
    await user.click(
      await screen.findByTitle("Make this the primary working directory"),
    );

    await waitFor(() => expect(updateMutateAsync).toHaveBeenCalledTimes(1));
    expect(updateMutateAsync).toHaveBeenCalledWith({
      resourceId: "r-web",
      data: { position: -1 },
    });
  });
});
