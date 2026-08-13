import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enProjects from "../../locales/en/projects.json";

const setConfig = vi.hoisted(() => vi.fn());
const resetConfig = vi.hoisted(() => vi.fn());

vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));

vi.mock("@agora/core/projects/queries", () => ({
  projectDetailOptions: () => ({
    queryKey: ["project", "p-1"],
    queryFn: async () => ({
      id: "p-1",
      settings: {
        orchestration: {
          execution_level: "auto",
          execution_strategy: "automatic",
          progression_policy: "automatic",
          max_concurrency: 3,
          review_plan_first: false,
        },
      },
    }),
  }),
  projectConfigOptions: () => ({
    queryKey: ["project", "p-1", "config"],
    queryFn: async () => [
      {
        key: "AGORA_AUTO_QA_ENABLED",
        kind: "bool",
        category: "QA",
        value: "false",
        overridden_by_project: true,
      },
      {
        key: "AGORA_AUTO_REVIEW_ENABLED",
        kind: "bool",
        category: "Review",
        value: "false",
        overridden_by_project: true,
      },
    ],
  }),
}));

vi.mock("@agora/core/projects/mutations", () => ({
  useUpdateProject: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useSetProjectConfig: () => ({ mutate: setConfig, isPending: false }),
  useResetProjectConfig: () => ({ mutate: resetConfig, isPending: false }),
}));

vi.mock("./project-pipeline-section", () => ({
  ProjectPipelineSection: () => <div>Advanced safeguards</div>,
}));

import { ProjectExecutionSection } from "./project-execution-section";

function renderSection() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider
        locale="en"
        resources={{ en: { common: enCommon, projects: enProjects } }}
      >
        <ProjectExecutionSection projectId="p-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("ProjectExecutionSection", () => {
  beforeEach(() => vi.clearAllMocks());

  it("shows a compact human-readable project workflow", async () => {
    const user = userEvent.setup();
    renderSection();

    await user.click(screen.getByRole("button", { name: /workflow/i }));

    expect(await screen.findByText("Build")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Default execution level" })).toHaveValue("auto");
    expect(screen.getByText("QA")).toBeInTheDocument();
    expect(screen.getByText("Review")).toBeInTheDocument();
    expect(screen.getByText("Release")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Human" })).toHaveLength(2);
    expect(screen.getByText("Human approval")).toBeInTheDocument();
    expect(screen.queryByText("Advanced safeguards")).not.toBeInTheDocument();
  });

  it("changes QA ownership from human to agent per project", async () => {
    const user = userEvent.setup();
    renderSection();
    await user.click(screen.getByRole("button", { name: /workflow/i }));

    const qaGroup = await screen.findByRole("group", { name: "QA" });
    await user.click(qaGroup.querySelectorAll("button")[1]!);

    expect(setConfig).toHaveBeenCalledWith(
      { key: "AGORA_AUTO_QA_ENABLED", value: "true" },
      expect.objectContaining({ onError: expect.any(Function) }),
    );
  });

  it("configures Preview and Checks per repository for multi-root projects", async () => {
    const user = userEvent.setup();
    renderSection();
    await user.click(screen.getByRole("button", { name: /workflow/i }));
    await user.click(await screen.findByRole("button", { name: /advanced orchestration/i }));

    expect(screen.getByText("Preview & checks")).toBeInTheDocument();
    expect(screen.getByText("Repository / folder overrides")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Add" }));
    expect(screen.getByRole("textbox", { name: "Repository or folder name" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Working directory inside repository" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Repository start command" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Repository test command" })).toBeInTheDocument();
  });
});
