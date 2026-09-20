import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enProjects from "../../locales/en/projects.json";
import { ProjectRiskMapSection } from "./project-risk-map-section";

// The risk-map editor (docs/orchestration-upgrade-plan.md §A1.2). What these
// tests pin: the module entries load and edit, the PUT carries the whole
// list, and a tier vocabulary this build has no copy for survives a save
// instead of being deleted.

const apiMocks = vi.hoisted(() => ({
  getProjectRiskMap: vi.fn(),
  updateProjectRiskMap: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const authEntry = {
  module: "auth",
  tier: "critical",
  paths: ["server/internal/auth/**"],
  owner: "",
  notes: "",
};

function mapResponse(over: Record<string, unknown> = {}) {
  return {
    project_id: "p-1",
    configured: true,
    risk_map: [authEntry],
    default_tier: "guarded",
    tiers: ["critical", "guarded", "safe"],
    max_entries: 40,
    ...over,
  };
}

function renderSection() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { projects: enProjects } }}>
      <QueryClientProvider client={qc}>
        <ProjectRiskMapSection projectId="p-1" />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("ProjectRiskMapSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.updateProjectRiskMap.mockImplementation((_id: string, entries: unknown) =>
      Promise.resolve(mapResponse({ risk_map: entries })),
    );
  });

  it("explains in one plain sentence what the tiers gate, naming the default", async () => {
    apiMocks.getProjectRiskMap.mockResolvedValue(mapResponse({ configured: false, risk_map: [] }));

    renderSection();

    expect(
      await screen.findByText(/deeper testing and a person's sign-off before it merges/),
    ).toBeInTheDocument();
    expect(screen.getByText(/no module matches is treated as guarded/)).toBeInTheDocument();
    expect(screen.getByText("No modules yet")).toBeInTheDocument();
  });

  it("loads the saved modules and saves an added path", async () => {
    const user = userEvent.setup();
    apiMocks.getProjectRiskMap.mockResolvedValue(mapResponse());

    renderSection();

    expect(await screen.findByText("server/internal/auth/**")).toBeInTheDocument();
    expect(screen.getByDisplayValue("auth")).toBeInTheDocument();
    // Nothing edited yet — Save stays inert.
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();

    await user.type(screen.getByLabelText("Add a path to auth"), "server/pkg/token/**{Enter}");
    expect(await screen.findByText("server/pkg/token/**")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(apiMocks.updateProjectRiskMap).toHaveBeenCalledWith("p-1", [
        {
          module: "auth",
          tier: "critical",
          paths: ["server/internal/auth/**", "server/pkg/token/**"],
          owner: "",
          notes: "",
        },
      ]),
    );
  });

  it("adds a module at the project's default tier and writes the whole list", async () => {
    const user = userEvent.setup();
    apiMocks.getProjectRiskMap.mockResolvedValue(mapResponse());

    renderSection();

    await user.click(await screen.findByRole("button", { name: "Add module" }));
    await user.type(screen.getAllByLabelText("Module name")[1]!, "billing");
    await user.type(screen.getByLabelText("Add a path to billing"), "app/billing/**{Enter}");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(apiMocks.updateProjectRiskMap).toHaveBeenCalledWith("p-1", [
        authEntry,
        { module: "billing", tier: "guarded", paths: ["app/billing/**"], owner: "", notes: "" },
      ]),
    );
  });

  it("removes a module, and removes a single path", async () => {
    const user = userEvent.setup();
    apiMocks.getProjectRiskMap.mockResolvedValue(
      mapResponse({
        risk_map: [
          authEntry,
          { module: "docs", tier: "safe", paths: ["docs/**"], owner: "", notes: "" },
        ],
      }),
    );

    renderSection();

    await user.click(await screen.findByRole("button", { name: "Remove docs" }));
    await user.click(screen.getByRole("button", { name: "Remove server/internal/auth/**" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(apiMocks.updateProjectRiskMap).toHaveBeenCalledWith("p-1", [
        { module: "auth", tier: "critical", paths: [], owner: "", notes: "" },
      ]),
    );
  });

  it("offers the server's tier vocabulary, including one it has no copy for", async () => {
    const user = userEvent.setup();
    apiMocks.getProjectRiskMap.mockResolvedValue(
      mapResponse({
        risk_map: [{ ...authEntry, tier: "nuclear" }],
        tiers: ["critical", "guarded", "safe"],
      }),
    );

    renderSection();

    const tier = await screen.findByLabelText("Risk tier for auth");
    // The entry's own tier is always offered, so editing cannot retier it.
    expect(tier).toHaveValue("nuclear");
    await user.selectOptions(tier, "guarded");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(apiMocks.updateProjectRiskMap).toHaveBeenCalledWith("p-1", [
        { ...authEntry, tier: "guarded" },
      ]),
    );
  });

  it("stops adding modules at the server's cap", async () => {
    apiMocks.getProjectRiskMap.mockResolvedValue(mapResponse({ max_entries: 1 }));

    renderSection();

    // Wait for the map itself to land — until it does, the editor has no
    // cap to enforce.
    await screen.findByDisplayValue("auth");
    expect(screen.getByRole("button", { name: "Add module" })).toBeDisabled();
    expect(screen.getByText("Module limit: 1")).toBeInTheDocument();
  });
});
