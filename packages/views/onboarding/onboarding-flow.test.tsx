import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import { RESOURCES } from "../locales";

const workspaces = vi.hoisted(() => ({ list: [] as Array<Record<string, unknown>> }));

vi.mock("@agora/core/workspace/queries", () => ({
  workspaceListOptions: () => ({ queryKey: ["workspaces", "list"], queryFn: async () => workspaces.list }),
}));
vi.mock("@agora/core/auth", () => ({
  useAuthStore: (selector: (s: { user: { id: string } }) => unknown) => selector({ user: { id: "u1" } }),
}));
vi.mock("@agora/core/analytics", () => ({ captureEvent: vi.fn() }));
vi.mock("./steps/step-welcome", () => ({ StepWelcome: () => <div>create-a-workspace welcome</div> }));
vi.mock("./member-setup/member-setup-flow", () => ({
  MemberSetupFlow: ({ workspaces: ws }: { workspaces: unknown[] }) => <div>member setup for {ws.length}</div>,
}));

import { OnboardingFlow } from "./onboarding-flow";

function renderFlow() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </I18nProvider>
  );
  return render(<OnboardingFlow onComplete={vi.fn()} />, { wrapper });
}

beforeEach(() => {
  workspaces.list = [];
});

describe("OnboardingFlow — who gets which setup", () => {
  it("sends someone already in an imported workspace to the member setup", async () => {
    workspaces.list = [
      { id: "ws-1", name: "Finance Department", slug: "finance-department", settings: { zoho_project_id: "229003" } },
      { id: "ws-2", name: "Billing Department", slug: "billing-department", settings: { zoho_project_id: "137003" } },
    ];
    renderFlow();
    expect(await screen.findByText("member setup for 2")).toBeInTheDocument();
    expect(screen.queryByText("create-a-workspace welcome")).not.toBeInTheDocument();
  });

  it("keeps the create-a-workspace flow for someone with no workspace or only their own", async () => {
    renderFlow();
    expect(await screen.findByText("create-a-workspace welcome")).toBeInTheDocument();
  });

  it("does not treat a workspace the person made themselves as imported", async () => {
    workspaces.list = [{ id: "ws-9", name: "My team", slug: "my-team", settings: {} }];
    renderFlow();
    expect(await screen.findByText("create-a-workspace welcome")).toBeInTheDocument();
    expect(screen.queryByText(/member setup/)).not.toBeInTheDocument();
  });
});
