import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
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

vi.mock("@agora/core/api", () => ({
  api: { getBaseUrl: () => "http://127.0.0.1:8080" },
}));

import { StepWorkspace } from "./step-workspace";

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
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

// The generic onboarding workspace step: pick an existing workspace, or
// create a new one with the name / slug fields.
describe("StepWorkspace — generic create + pick-existing flow", () => {
  it("renders the create form when there is no existing workspace", () => {
    renderStep({ existing: null, disabled: false });

    expect(
      screen.getByText("Name your workspace.", { exact: false }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Workspace name")).toBeInTheDocument();
    expect(screen.getByLabelText("URL")).toBeInTheDocument();
  });

  it("shows both pick-existing and create-new cards when a workspace already exists", () => {
    renderStep({ existing: EXISTING_WORKSPACE, disabled: false });

    // The existing workspace is offered as a resume card.
    expect(screen.getAllByText("Acme").length).toBeGreaterThan(0);
    // The create-new radio card is available alongside it.
    expect(
      screen.getByText("Create a new workspace", { exact: false }),
    ).toBeInTheDocument();
  });
});

// Regression for #3433 (PR feedback): when DISABLE_WORKSPACE_CREATION is on,
// every onboarding entry point must steer the user toward an existing
// workspace or a logout escape — never toward the create form, even
// indirectly (stale CTA copy, "or start another" prose, etc.).
describe("StepWorkspace — DISABLE_WORKSPACE_CREATION gate", () => {
  it("hides the create form and shows the disabled notice when the flag is on and there is no workspace", () => {
    renderStep({ existing: null, disabled: true });

    expect(
      screen.getByText("Ask your administrator for an invitation.", {
        exact: false,
      }),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("Workspace name")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("URL")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /log out/i })).toBeInTheDocument();
  });

  it("forces the existing-workspace-only state when the flag is on and the user already has a workspace", () => {
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

    // Resume picker still shows the existing workspace card, but the
    // "Create a new workspace" radio card is gone entirely.
    expect(screen.getAllByText("Acme").length).toBeGreaterThan(0);
    expect(
      screen.queryByText("Create a new workspace", { exact: false }),
    ).not.toBeInTheDocument();

    // CTA is pre-selected to the existing-only action and immediately
    // enabled, so the user can press it to advance into their workspace.
    const cta = screen.getByRole("button", { name: "Open Acme" });
    expect(cta).toBeEnabled();
    fireEvent.click(cta);
    expect(onCreated).toHaveBeenCalledTimes(1);
    expect(onCreated).toHaveBeenCalledWith(EXISTING_WORKSPACE);
  });
});
