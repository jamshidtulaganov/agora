import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import { useAssistantStore } from "@agora/core/assistant";
import { api } from "@agora/core/api";
import type { Attachment } from "@agora/core/types";
import { RESOURCES } from "../../locales";
import { AssistantComposeResources } from "./compose-resources";

vi.mock("@agora/core/assistant", async () => {
  const { create } = await import("zustand");
  const useAssistantStore = create<{
    composerContextBySession: Record<string, { workspace_id: string | null; project_id?: string | null; attachments?: { id: string; filename: string; size_bytes: number }[] }>;
    setComposerContext: (id: string, value: { workspace_id: string | null; project_id?: string | null; attachments?: { id: string; filename: string; size_bytes: number }[] } | null) => void;
  }>((set) => ({
    composerContextBySession: {},
    setComposerContext: (id, value) => set((state) => {
      const next = { ...state.composerContextBySession };
      if (value) next[id] = value;
      else delete next[id];
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
vi.mock("@agora/core/api", () => ({ api: { uploadFile: vi.fn() } }));

function renderControls(onUploadingChange = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={client}>
        <AssistantComposeResources sessionId="session-1" workspaceId="workspace-1" onUploadingChange={onUploadingChange} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

beforeEach(() => {
  useAssistantStore.setState({ composerContextBySession: {} });
  vi.mocked(api.uploadFile).mockReset();
});

describe("AssistantComposeResources", () => {
  it("selects a project and shows linked resource names without claiming contents are loaded", async () => {
    renderControls();
    await userEvent.click(screen.getByRole("button", { name: "Project" }));
    await userEvent.click(await screen.findByText("Website"));

    expect(useAssistantStore.getState().composerContextBySession["session-1"]?.project_id).toBe("project-1");
    expect(await screen.findByLabelText("Project resources for Website")).toHaveTextContent("Site repo");
    expect(screen.getByLabelText("Project resources for Website")).toHaveTextContent("repository contents are not loaded automatically");
    await userEvent.click(screen.getByRole("button", { name: "Remove project" }));
    expect(useAssistantStore.getState().composerContextBySession["session-1"]?.project_id).toBeUndefined();
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
