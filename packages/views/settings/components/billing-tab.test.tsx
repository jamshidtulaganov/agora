import { type ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const subRef = vi.hoisted(() => ({ current: undefined as Record<string, unknown> | undefined }));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: subRef.current, isLoading: false }),
}));

vi.mock("@agora/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@agora/core/billing", () => ({
  workspaceSubscriptionOptions: () => ({ queryKey: ["workspace-subscription", "workspace-1"] }),
  isUnlimited: (n: number) => n < 0,
}));

import { BillingTab } from "./billing-tab";

const TEST_RESOURCES = { en: { common: enCommon, settings: enSettings } };

function wrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

const PLANS = [
  { id: "free", price_monthly_cents: 0, price_yearly_cents: 0, limits: { automations: 2, report_schedules: 1, autopilots: 0 } },
  { id: "team", price_monthly_cents: 1500, price_yearly_cents: 1200, limits: { automations: -1, report_schedules: -1, autopilots: -1 } },
];

function base(over: Record<string, unknown>) {
  return {
    enforced: true,
    plan: "team",
    purchased_plan: "team",
    status: "active",
    billing_interval: "",
    seats_paid: 0,
    trial_ends_at: null,
    trial_days_left: 0,
    current_period_end: null,
    grace_ends_at: null,
    limits: { automations: -1, report_schedules: -1, autopilots: -1 },
    usage: { seats: 3, automations: 4, report_schedules: 0, autopilots: 1 },
    plans: PLANS,
    contact: "sales@example.com",
    ...over,
  };
}

describe("BillingTab", () => {
  beforeEach(() => {
    subRef.current = undefined;
  });

  it("shows the trial with days left and an upgrade link", () => {
    subRef.current = base({ status: "trialing", trial_ends_at: "2026-12-04T12:00:00Z", trial_days_left: 14 });
    render(<BillingTab />, { wrapper });
    expect(screen.getByText(/14 days left/)).toBeTruthy();
    expect(screen.getByText(/Your Team trial ends on/)).toBeTruthy();
    const link = screen.getByText("Contact us to upgrade").closest("a");
    expect(link?.getAttribute("href")).toBe("mailto:sales@example.com");
  });

  it("explains Free after the trial and flags usage over the limit", () => {
    subRef.current = base({
      plan: "free",
      status: "trial_expired",
      limits: { automations: 2, report_schedules: 1, autopilots: 0 },
    });
    render(<BillingTab />, { wrapper });
    expect(screen.getByText(/Your Team trial has ended/)).toBeTruthy();
    expect(screen.getByText(/keep working on Free/)).toBeTruthy();
    expect(screen.getByText("4 of 2")).toBeTruthy();
    expect(screen.getAllByText(/Over the Free limit/).length).toBe(2);
  });

  it("hides the upgrade card for a paid team", () => {
    subRef.current = base({});
    render(<BillingTab />, { wrapper });
    expect(screen.queryByText("Contact us to upgrade")).toBeNull();
  });
});
