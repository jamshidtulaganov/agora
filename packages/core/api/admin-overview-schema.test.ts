import { describe, expect, it } from "vitest";
import { AdminOverviewSchema, EMPTY_ADMIN_OVERVIEW } from "./schemas";
import { parseWithFallback } from "./schema";

const parse = (raw: unknown) =>
  parseWithFallback(raw, AdminOverviewSchema, EMPTY_ADMIN_OVERVIEW, { endpoint: "test" });

describe("AdminOverviewSchema", () => {
  it("parses a normal overview", () => {
    const out = parse({
      summary: { users: 3, paying: 1, est_monthly_revenue_cents: 6000 },
      users: [{ id: "u1", name: "A", email: "a@x.test", workspaces: [{ slug: "s", name: "S", role: "owner" }] }],
      workspaces: [{ id: "w1", name: "S", slug: "s", members: 2, subscription: { plan: "team", status: "trialing", trial_days_left: 3 } }],
    });
    expect(out.summary.users).toBe(3);
    expect(out.users[0]?.workspaces[0]?.role).toBe("owner");
    expect(out.workspaces[0]?.subscription.trial_days_left).toBe(3);
  });

  it("degrades bad fields without dropping rows", () => {
    const out = parse({
      summary: null,
      users: [{ id: "u1", email: 5, workspaces: null, last_login_at: 12 }],
      workspaces: [{ id: "w1", subscription: "nope", members: "2" }],
    });
    expect(out.summary.users).toBe(0);
    expect(out.users).toHaveLength(1);
    expect(out.users[0]?.workspaces).toEqual([]);
    expect(out.users[0]?.last_login_at).toBeNull();
    expect(out.workspaces[0]?.subscription.plan).toBe("");
    expect(out.workspaces[0]?.members).toBe(0);
  });

  it("falls back to empty for a non-object body", () => {
    expect(parse("oops")).toEqual(EMPTY_ADMIN_OVERVIEW);
  });
});

import { AdminGrowthSchema, EMPTY_ADMIN_GROWTH } from "./schemas";

describe("AdminGrowthSchema", () => {
  const parseGrowth = (raw: unknown) =>
    parseWithFallback(raw, AdminGrowthSchema, EMPTY_ADMIN_GROWTH, { endpoint: "test" });

  it("parses days and funnel rows", () => {
    const out = parseGrowth({
      days: [{ day: "2026-11-20", signups: 3, active_users: 9, new_workspaces: 1 }],
      signups: 3,
      attributed: 2,
      sources: [{ key: "telegram / post", signups: 2, owns_workspace: 1, paying: 1 }],
    });
    expect(out.days[0]?.active_users).toBe(9);
    expect(out.sources[0]?.paying).toBe(1);
    expect(out.campaigns).toEqual([]);
  });

  it("degrades malformed fields", () => {
    const out = parseGrowth({ days: null, sources: [{ key: 7, signups: "x" }], attributed: "2" });
    expect(out.days).toEqual([]);
    expect(out.sources[0]?.key).toBe("");
    expect(out.sources[0]?.signups).toBe(0);
    expect(out.attributed).toBe(0);
  });
});
