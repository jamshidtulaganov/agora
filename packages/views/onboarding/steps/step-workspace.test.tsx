import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enOnboarding from "../../locales/en/onboarding.json";
import enWorkspace from "../../locales/en/workspace.json";
import type { Workspace } from "@agora/core/types";

const TEST_RESOURCES = {
  en: {
    common: enCommon,
    onboarding: enOnboarding,
    workspace: enWorkspace,
  },
};

const mockLogout = vi.hoisted(() => vi.fn());
const mockUseConfigStore = vi.hoisted(() =>
  vi.fn(
    (selector: (state: { workspaceCreationDisabled: boolean }) => unknown) =>
      selector({ workspaceCreationDisabled: false }),
  ),
);

vi.mock("../../auth", () => ({
  useLogout: () => mockLogout,
}));

vi.mock("@agora/core/config", () => ({
  useConfigStore: (selector: (state: { workspaceCreationDisabled: boolean }) => unknown) =>
    mockUseConfigStore(selector),
}));

vi.mock("@agora/core/workspace/mutations", () => ({
  useCreateWorkspace: () => ({ mutate: vi.fn(), isPending: false }),
}));

const mockListWorkspaces = vi.hoisted(() => vi.fn());
const mockCreateWorkspace = vi.hoisted(() => vi.fn());
const mockUpdateWorkspace = vi.hoisted(() => vi.fn());

vi.mock("@agora/core/api", () => ({
  api: {
    getBaseUrl: () => "http://127.0.0.1:8080",
    listWorkspaces: (...args: unknown[]) => mockListWorkspaces(...args),
    createWorkspace: (...args: unknown[]) => mockCreateWorkspace(...args),
    updateWorkspace: (...args: unknown[]) => mockUpdateWorkspace(...args),
  },
}));

import { StepWorkspace } from "./step-workspace";

function I18nWrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return (
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        {children}
      </I18nProvider>
    </QueryClientProvider>
  );
}

function renderStep({
  existing,
  disabled,
}: {
  existing: Workspace | null;
  disabled: boolean;
}) {
  mockUseConfigStore.mockImplementation(
    (selector: (state: { workspaceCreationDisabled: boolean }) => unknown) =>
      selector({ workspaceCreationDisabled: disabled }),
  );
  return render(
    <StepWorkspace existing={existing} onCreated={vi.fn()} onBack={vi.fn()} />,
    { wrapper: I18nWrapper },
  );
}

const EXISTING_WORKSPACE: Workspace = {
  id: "00000000-0000-0000-0000-000000000001",
  name: "Acme",
  slug: "acme",
  description: null,
  context: null,
  settings: {},
  repos: [],
  issue_prefix: "ACM",
  created_at: "2025-01-01T00:00:00Z",
  updated_at: "2025-01-01T00:00:00Z",
} as unknown as Workspace;

// Regression for #3433 (PR feedback): when DISABLE_WORKSPACE_CREATION is on,
// every onboarding entry point must steer the user toward an existing
// workspace or a logout escape — never toward the create form, even
// indirectly (stale CTA copy, "or start another" prose, etc.).
describe("StepWorkspace — DISABLE_WORKSPACE_CREATION gate", () => {
  it("renders the create form when the flag is off and the user has no workspace", () => {
    renderStep({ existing: null, disabled: false });

    expect(
      screen.getByText("Name your workspace.", { exact: false }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Workspace name")).toBeInTheDocument();
    expect(screen.getByLabelText("URL")).toBeInTheDocument();
  });

  it("hides the create form and shows the disabled notice when the flag is on and there is no workspace", () => {
    renderStep({ existing: null, disabled: true });

    expect(
      screen.getByText("Ask your administrator for an invitation.", {
        exact: false,
      }),
    ).toBeInTheDocument();
    // No create UI: neither the bulk "Create all 3" action nor the manual form.
    expect(screen.queryByLabelText("Workspace name")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("URL")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /create all 3 workspaces/i }),
    ).not.toBeInTheDocument();
    // Read-only note lists the three SD workspaces the user belongs to.
    expect(
      screen.getByText("Your SalesDoctor workspaces", { exact: false }),
    ).toBeInTheDocument();
    expect(screen.getByText("sd-main")).toBeInTheDocument();
    expect(screen.getByText("sd-cs")).toBeInTheDocument();
    expect(screen.getByText("sd-billing")).toBeInTheDocument();
    // With no resolved existing workspace, the logout escape is the only action
    // (the user is never trapped — preserves #3433 intent).
    expect(screen.getByRole("button", { name: /log out/i })).toBeInTheDocument();
  });

  it("shows the read-only note + a working Continue when the flag is on and the user has a workspace", () => {
    const onCreated = vi.fn();
    mockUseConfigStore.mockImplementation(
      (selector: (state: { workspaceCreationDisabled: boolean }) => unknown) =>
        selector({ workspaceCreationDisabled: true }),
    );
    render(
      <StepWorkspace
        existing={EXISTING_WORKSPACE}
        onCreated={onCreated}
        onBack={vi.fn()}
      />,
      { wrapper: I18nWrapper },
    );

    // Disabled-specific copy is used in place of the "or start another" prose.
    expect(
      screen.getByText("Continue with Acme.", { exact: false }),
    ).toBeInTheDocument();
    expect(screen.queryByText(/start another/i)).not.toBeInTheDocument();
    expect(
      screen.queryByText(/create a new one alongside it/i),
    ).not.toBeInTheDocument();

    // No create affordances at all: no bulk action, no manual form inputs, and
    // no "Create a new workspace" radio card.
    expect(
      screen.queryByRole("button", { name: /create all 3 workspaces/i }),
    ).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Workspace name")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("URL")).not.toBeInTheDocument();
    expect(
      screen.queryByText("Create a new workspace", { exact: false }),
    ).not.toBeInTheDocument();

    // The read-only note lists the three SD workspaces.
    expect(
      screen.getByText("Your SalesDoctor workspaces", { exact: false }),
    ).toBeInTheDocument();
    expect(screen.getByText("sd-main")).toBeInTheDocument();
    expect(screen.getByText("sd-cs")).toBeInTheDocument();
    expect(screen.getByText("sd-billing")).toBeInTheDocument();

    // A single Continue button, enabled, advances into the existing workspace.
    const cta = screen.getByRole("button", { name: /continue/i });
    expect(cta).toBeEnabled();
    fireEvent.click(cta);
    expect(onCreated).toHaveBeenCalledTimes(1);
    expect(onCreated).toHaveBeenCalledWith(EXISTING_WORKSPACE);
  });
});

// SD fork: the "Create all 3 workspaces" primary action seeds the three
// SalesDoctor sibling projects (sd-main / sd-cs / sd-billing), attaches each
// repo idempotently, and lands the user on sd-main.
describe("StepWorkspace — SD bulk-create primary action", () => {
  function wsFixture(slug: string, repos: { url: string }[] = []): Workspace {
    return {
      id: `id-${slug}`,
      name: slug,
      slug,
      description: null,
      context: null,
      settings: {},
      repos,
      issue_prefix: slug.slice(0, 3).toUpperCase(),
      avatar_url: null,
      created_at: "2025-01-01T00:00:00Z",
      updated_at: "2025-01-01T00:00:00Z",
    } as unknown as Workspace;
  }

  beforeEach(() => {
    mockListWorkspaces.mockReset();
    mockCreateWorkspace.mockReset();
    mockUpdateWorkspace.mockReset();
    mockUseConfigStore.mockImplementation(
      (selector: (state: { workspaceCreationDisabled: boolean }) => unknown) =>
        selector({ workspaceCreationDisabled: false }),
    );
  });

  function renderWithOnCreated() {
    const onCreated = vi.fn();
    render(
      <StepWorkspace existing={null} onCreated={onCreated} onBack={vi.fn()} />,
      { wrapper: I18nWrapper },
    );
    return { onCreated };
  }

  it("renders the primary action and lists all three SD workspaces", () => {
    renderWithOnCreated();
    expect(
      screen.getByRole("button", { name: /create all 3 workspaces/i }),
    ).toBeInTheDocument();
    // Each sibling project name appears in the primary block list.
    expect(screen.getByText("sd-main")).toBeInTheDocument();
    expect(screen.getByText("sd-cs")).toBeInTheDocument();
    expect(screen.getByText("sd-billing")).toBeInTheDocument();
  });

  it("creates all three workspaces, attaches repos, and lands on sd-main", async () => {
    mockListWorkspaces.mockResolvedValue([]);
    mockCreateWorkspace.mockImplementation(
      ({ slug }: { slug: string }) => Promise.resolve(wsFixture(slug)),
    );
    mockUpdateWorkspace.mockImplementation((id: string) =>
      Promise.resolve(wsFixture(id.replace(/^id-/, ""), [{ url: "x" }])),
    );

    const { onCreated } = renderWithOnCreated();
    fireEvent.click(
      screen.getByRole("button", { name: /create all 3 workspaces/i }),
    );

    await waitFor(() => expect(onCreated).toHaveBeenCalledTimes(1));

    // One create per sibling project, with slug == name.
    expect(mockCreateWorkspace).toHaveBeenCalledTimes(3);
    const createdSlugs = mockCreateWorkspace.mock.calls.map(([a]) => a.slug);
    expect(createdSlugs).toEqual(["sd-main", "sd-cs", "sd-billing"]);

    // Each fresh workspace gets exactly one repo attached.
    expect(mockUpdateWorkspace).toHaveBeenCalledTimes(3);
    mockUpdateWorkspace.mock.calls.forEach(([, data]) => {
      expect(Array.isArray(data.repos)).toBe(true);
      expect(data.repos).toHaveLength(1);
      expect(data.repos[0].url).toMatch(/^https:\/\/github\.com\//);
    });

    // Lands on sd-main (the system of record).
    const [landed] = onCreated.mock.calls[0]!;
    expect(landed.slug).toBe("sd-main");
  });

  it("dedups against existing workspaces by slug and skips repo set when repos already present", async () => {
    // sd-main already exists WITH a repo → reuse, no create, no repo clobber.
    mockListWorkspaces.mockResolvedValue([
      wsFixture("sd-main", [{ url: "https://github.com/azizkh/sd" }]),
    ]);
    mockCreateWorkspace.mockImplementation(
      ({ slug }: { slug: string }) => Promise.resolve(wsFixture(slug)),
    );
    mockUpdateWorkspace.mockImplementation((id: string) =>
      Promise.resolve(wsFixture(id.replace(/^id-/, ""), [{ url: "x" }])),
    );

    const { onCreated } = renderWithOnCreated();
    fireEvent.click(
      screen.getByRole("button", { name: /create all 3 workspaces/i }),
    );

    await waitFor(() => expect(onCreated).toHaveBeenCalledTimes(1));

    // Only the two missing siblings are created.
    const createdSlugs = mockCreateWorkspace.mock.calls.map(([a]) => a.slug);
    expect(createdSlugs).toEqual(["sd-cs", "sd-billing"]);

    // sd-main already had a repo, so it is never re-set (only the 2 new ones).
    const updatedIds = mockUpdateWorkspace.mock.calls.map(([id]) => id);
    expect(updatedIds).not.toContain("id-sd-main");
    expect(updatedIds).toHaveLength(2);

    // Still lands on the reused sd-main.
    const [landed] = onCreated.mock.calls[0]!;
    expect(landed.slug).toBe("sd-main");
  });

  it("is best-effort: still lands on sd-main even if repo attach fails", async () => {
    mockListWorkspaces.mockResolvedValue([]);
    mockCreateWorkspace.mockImplementation(
      ({ slug }: { slug: string }) => Promise.resolve(wsFixture(slug)),
    );
    mockUpdateWorkspace.mockRejectedValue(new Error("network down"));

    const { onCreated } = renderWithOnCreated();
    fireEvent.click(
      screen.getByRole("button", { name: /create all 3 workspaces/i }),
    );

    await waitFor(() => expect(onCreated).toHaveBeenCalledTimes(1));
    expect(mockCreateWorkspace).toHaveBeenCalledTimes(3);
    const [landed] = onCreated.mock.calls[0]!;
    expect(landed.slug).toBe("sd-main");
  });
});
