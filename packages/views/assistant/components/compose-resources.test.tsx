import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import { useAssistantStore } from "@agora/core/assistant";
import { api } from "@agora/core/api";
import type { Attachment } from "@agora/core/types";
import { RESOURCES } from "../../locales";
import { useAssistantComposeResources } from "./compose-resources";

// The hook's three parts render in different spots inside the composer; the
// harness stacks them so every picker, pill and notice is on screen.
function ComposeResourcesHarness(props: Parameters<typeof useAssistantComposeResources>[0]) {
  const { toolbar, attachments, notices } = useAssistantComposeResources(props);
  return <>{attachments}{toolbar}{notices}</>;
}

interface TestSelection {
  workspace_id: string | null;
  workspace_pinned?: boolean;
  project_id?: string | null;
  member?: { user_id: string; name: string } | null;
  attachments?: { id: string; filename: string; size_bytes: number }[];
}

vi.mock("@agora/core/assistant", async () => {
  const { create } = await import("zustand");
  const useAssistantStore = create<{
    composerContextBySession: Record<string, TestSelection>;
    setComposerContext: (id: string, value: TestSelection | null) => void;
  }>((set) => ({
    composerContextBySession: {},
    // Mirrors the real store: everything scoped BY the workspace is dropped
    // when the workspace moves.
    setComposerContext: (id, value) => set((state) => {
      const next = { ...state.composerContextBySession };
      if (value) {
        const previous = state.composerContextBySession[id];
        const workspaceChanged = previous !== undefined && previous.workspace_id !== value.workspace_id;
        next[id] = workspaceChanged
          ? { ...value, project_id: null, member: null, attachments: [] }
          : value;
      } else {
        delete next[id];
      }
      return { composerContextBySession: next };
    }),
  }));
  return { useAssistantStore };
});

vi.mock("@agora/core/projects/queries", () => ({
  projectListOptions: (wsId: string) => ({
    queryKey: ["projects", wsId],
    queryFn: async () => [{ id: "project-1", title: "Website" }],
  }),
}));
vi.mock("@agora/core/projects", () => ({
  projectResourcesOptions: (_wsId: string, projectId: string) => ({
    queryKey: ["project-resources", projectId],
    queryFn: async () => [{ id: "resource-1", resource_type: "github_repo", resource_ref: { url: "https://github.com/example/site" }, label: "Site repo" }],
  }),
}));
vi.mock("@agora/core/workspace", () => ({
  workspaceListOptions: () => ({
    queryKey: ["workspaces", "list"],
    queryFn: async () => [
      { id: "workspace-1", slug: "acme", name: "Acme" },
      { id: "workspace-2", slug: "beta", name: "Beta" },
    ],
  }),
  memberListOptions: (wsId: string) => ({
    queryKey: ["workspaces", wsId, "members"],
    queryFn: async () =>
      wsId === "workspace-1"
        ? [
            { user_id: "user-1", name: "Dana Ruiz", email: "dana@agora.dev" },
            { user_id: "user-2", name: "Sam Cole", email: "sam@agora.dev" },
          ]
        : [{ user_id: "user-9", name: "Beta Person", email: "beta@agora.dev" }],
  }),
}));
vi.mock("@agora/core/api", () => ({ api: { uploadFile: vi.fn() } }));

function renderControls(onUploadingChange = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={client}>
        <ComposeResourcesHarness sessionId="session-1" workspaceId="workspace-1" onUploadingChange={onUploadingChange} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

beforeEach(() => {
  useAssistantStore.setState({ composerContextBySession: {} });
  vi.mocked(api.uploadFile).mockReset();
});

describe("useAssistantComposeResources", () => {
  it("selects a project and shows linked resource names without claiming contents are loaded", async () => {
    renderControls();
    await userEvent.click(screen.getByRole("button", { name: "Project" }));
    await userEvent.click(await screen.findByText("Website"));

    expect(useAssistantStore.getState().composerContextBySession["session-1"]?.project_id).toBe("project-1");
    expect(await screen.findByLabelText("Project resources for Website")).toHaveTextContent("Site repo");
    expect(screen.getByLabelText("Project resources for Website")).toHaveTextContent("repository contents are not loaded automatically");
    await userEvent.click(screen.getByRole("button", { name: "Remove project" }));
    expect(useAssistantStore.getState().composerContextBySession["session-1"]?.project_id).toBeNull();
  });

  it("uploads a small text file in the selected workspace and can remove it", async () => {
    let resolveUpload!: (value: Attachment) => void;
    vi.mocked(api.uploadFile).mockReturnValue(new Promise<Attachment>((resolve) => { resolveUpload = resolve; }));
    const onUploadingChange = vi.fn();
    const { container } = renderControls(onUploadingChange);
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [new File(["hello"], "notes.md", { type: "text/markdown" })] } });
    await waitFor(() => expect(onUploadingChange).toHaveBeenCalledWith(true));
    expect(api.uploadFile).toHaveBeenCalledWith(expect.any(File), { workspaceId: "workspace-1" });
    resolveUpload({ id: "file-1", workspace_id: "workspace-1", filename: "notes.md", size_bytes: 5 } as Attachment);
    expect(await screen.findByText("notes.md")).toBeInTheDocument();
    expect(useAssistantStore.getState().composerContextBySession["session-1"]?.attachments?.[0]?.id).toBe("file-1");
    await userEvent.click(screen.getByRole("button", { name: "Remove notes.md" }));
    expect(useAssistantStore.getState().composerContextBySession["session-1"]?.attachments).toEqual([]);
  });

  it("rejects oversized files before upload", async () => {
    const { container } = renderControls();
    fireEvent.change(container.querySelector('input[type="file"]')!, {
      target: { files: [new File([new Uint8Array(64 * 1024 + 1)], "huge.txt")] },
    });
    expect(await screen.findByRole("alert")).toHaveTextContent("64 KiB");
    expect(api.uploadFile).not.toHaveBeenCalled();
  });
});

describe("useAssistantComposeResources — workspace picker", () => {
  it("sends the next message to the picked workspace instead of the page's", async () => {
    renderControls();
    // The chip shows where the message is going right now: the page's own.
    await screen.findByRole("button", { name: "Acme" });

    await userEvent.click(screen.getByRole("button", { name: "Acme" }));
    await userEvent.click(await screen.findByText("Beta"));

    const selection = useAssistantStore.getState().composerContextBySession["session-1"];
    expect(selection).toMatchObject({ workspace_id: "workspace-2", workspace_pinned: true });
    expect(await screen.findByRole("button", { name: "Beta" })).toBeInTheDocument();

    // Removing the pick falls back to the page's workspace rather than
    // leaving the composer pointing nowhere.
    await userEvent.click(screen.getByRole("button", { name: "Use the current workspace" }));
    expect(useAssistantStore.getState().composerContextBySession["session-1"]).toMatchObject({
      workspace_id: "workspace-1",
      workspace_pinned: false,
    });
    expect(await screen.findByRole("button", { name: "Acme" })).toBeInTheDocument();
  });

  it("drops a project and files that belonged to the workspace being left", async () => {
    renderControls();
    await userEvent.click(screen.getByRole("button", { name: "Project" }));
    await userEvent.click(await screen.findByText("Website"));
    expect(useAssistantStore.getState().composerContextBySession["session-1"]?.project_id).toBe("project-1");

    await userEvent.click(await screen.findByRole("button", { name: "Acme" }));
    await userEvent.click(await screen.findByText("Beta"));

    const selection = useAssistantStore.getState().composerContextBySession["session-1"];
    expect(selection?.project_id).toBeNull();
    expect(selection?.attachments).toEqual([]);
  });
});

describe("useAssistantComposeResources — member picker", () => {
  it("attaches a teammate from the target workspace and can remove them", async () => {
    renderControls();
    await userEvent.click(screen.getByRole("button", { name: "Person" }));
    await userEvent.click(await screen.findByText("Dana Ruiz"));

    expect(useAssistantStore.getState().composerContextBySession["session-1"]?.member).toEqual({
      user_id: "user-1",
      name: "Dana Ruiz",
    });
    // The chip and the hint both name the person, so "assign it to her" is
    // unambiguous before the message is even sent.
    expect(await screen.findByRole("button", { name: "Dana Ruiz" })).toBeInTheDocument();
    expect(screen.getByText(/Dana Ruiz is attached/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Remove person" }));
    expect(useAssistantStore.getState().composerContextBySession["session-1"]?.member).toBeNull();
    expect(screen.getByRole("button", { name: "Person" })).toBeInTheDocument();
  });

  // The roster follows the workspace picker, not the page: attaching somebody
  // the target workspace has never heard of is refused by the server.
  it("lists the picked workspace's roster", async () => {
    renderControls();
    await userEvent.click(await screen.findByRole("button", { name: "Acme" }));
    await userEvent.click(await screen.findByText("Beta"));

    await userEvent.click(screen.getByRole("button", { name: "Person" }));
    expect(await screen.findByText("Beta Person")).toBeInTheDocument();
    expect(screen.queryByText("Dana Ruiz")).not.toBeInTheDocument();
  });
});
