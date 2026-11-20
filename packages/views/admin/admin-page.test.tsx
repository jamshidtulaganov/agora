import { type ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../locales/en/common.json";
import enAdmin from "../locales/en/admin.json";

const ApiError = vi.hoisted(
  () =>
    class ApiError extends Error {
      constructor(message: string, readonly status: number) {
        super(message);
      }
    },
);
const queryRef = vi.hoisted(() => ({ current: {} as Record<string, unknown> }));
const growthRef = vi.hoisted(() => ({ current: {} as Record<string, unknown> }));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[] }) =>
    opts.queryKey[1] === "growth" ? growthRef.current : queryRef.current,
}));
vi.mock("@agora/core/api", () => ({ ApiError }));
vi.mock("@agora/core/platform-admin", () => ({
  adminOverviewOptions: () => ({ queryKey: ["platform-admin", "overview"] }),
  adminGrowthOptions: (days: number) => ({ queryKey: ["platform-admin", "growth", days] }),
  useSetAdminWorkspacePlan: () => ({ mutate: vi.fn(), isPending: false }),
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { AdminPage } from "./admin-page";

function wrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={{ en: { common: enCommon, admin: enAdmin } }}>
      {children}
    </I18nProvider>
  );
}

const overview = {
  summary: {
    users: 3, active_users_7d: 2, active_users_30d: 2, new_users_7d: 1, workspaces: 2,
    trialing: 1, trials_ending_7d: 1, paying: 1, past_due: 0, free: 0, est_monthly_revenue_cents: 6000,
  },
  users: [
    { id: "u1", name: "Dilnoza", email: "d@x.test", created_at: null, last_login_at: null, last_active_at: null, workspaces: [] },
  ],
  workspaces: [
    {
      id: "w1", name: "Acme", slug: "acme", created_at: null, members: 4, owner_email: "o@x.test",
      last_active_at: null, provider: "manual", note: "INV-1",
      subscription: {
        enforced: true, plan: "team", purchased_plan: "team", status: "trialing", billing_interval: "",
        seats_paid: 0, trial_ends_at: "2026-12-04T00:00:00Z", trial_days_left: 3,
        current_period_end: null, grace_ends_at: null,
      },
    },
  ],
};

describe("AdminPage", () => {
  beforeEach(() => {
    queryRef.current = {};
    growthRef.current = {
      isLoading: false,
      error: null,
      data: {
        days: [{ day: "2026-11-20", signups: 2, active_users: 5, new_workspaces: 1 }],
        signups: 2,
        attributed: 1,
        sources: [{ key: "telegram / post", signups: 1, owns_workspace: 1, paying: 1 }, { key: "(direct)", signups: 1, owns_workspace: 0, paying: 0 }],
        campaigns: [],
        referrers: [],
        landing_pages: [],
      },
    };
  });

  it("shows not found when the API hides the page", () => {
    queryRef.current = { isLoading: false, data: undefined, error: new ApiError("nf", 404), refetch: vi.fn() };
    render(<AdminPage />, { wrapper });
    expect(screen.getByText("Page not found.")).toBeTruthy();
  });

  it("renders the dashboard numbers and workspace rows", () => {
    queryRef.current = { isLoading: false, data: overview, error: null, refetch: vi.fn(), isFetching: false };
    render(<AdminPage />, { wrapper });
    expect(screen.getByText("$60")).toBeTruthy();
    fireEvent.click(screen.getByRole("tab", { name: /Workspaces/ }));
    expect(screen.getByText("Acme")).toBeTruthy();
    expect(screen.getByText(/3 d left/)).toBeTruthy();
    expect(screen.getByText("INV-1")).toBeTruthy();
  });

  it("shows the marketing funnel by source", () => {
    queryRef.current = { isLoading: false, data: overview, error: null, refetch: vi.fn(), isFetching: false };
    render(<AdminPage />, { wrapper });
    fireEvent.click(screen.getByRole("tab", { name: "Marketing" }));
    expect(screen.getByText("1 of 2 sign-ups have a known source.")).toBeTruthy();
    expect(screen.getByText("telegram / post")).toBeTruthy();
    expect(screen.getAllByText("Direct / unknown").length).toBeGreaterThan(0);
  });
});
