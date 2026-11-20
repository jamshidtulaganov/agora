import { describe, expect, it } from "vitest";
import { EMPTY_WORKSPACE_SUBSCRIPTION, WorkspaceSubscriptionSchema } from "./schemas";
import { parseWithFallback } from "./schema";

const parse = (raw: unknown) =>
  parseWithFallback(raw, WorkspaceSubscriptionSchema, EMPTY_WORKSPACE_SUBSCRIPTION, {
    endpoint: "test",
  });

describe("WorkspaceSubscriptionSchema", () => {
  it("parses a trialing workspace", () => {
    const out = parse({
      enforced: true,
      plan: "team",
      purchased_plan: "team",
      status: "trialing",
      trial_ends_at: "2026-12-04T12:00:00Z",
      trial_days_left: 14,
      limits: { automations: -1, report_schedules: -1, autopilots: -1 },
      usage: { seats: 3, automations: 1, report_schedules: 0, autopilots: 0 },
      plans: [{ id: "free", limits: { automations: 2, report_schedules: 1, autopilots: 0 } }],
      contact: "sales@example.com",
    });
    expect(out.status).toBe("trialing");
    expect(out.trial_days_left).toBe(14);
    expect(out.plans[0]?.limits.automations).toBe(2);
  });

  it("keeps unknown plan and status values instead of rejecting", () => {
    const out = parse({ plan: "scale", status: "paused" });
    expect(out.plan).toBe("scale");
    expect(out.status).toBe("paused");
  });

  it("degrades malformed fields to permissive defaults", () => {
    const out = parse({
      enforced: "yes",
      limits: null,
      usage: "nope",
      plans: null,
      trial_days_left: "3",
    });
    expect(out.enforced).toBe(false);
    expect(out.limits.automations).toBe(-1);
    expect(out.usage.seats).toBe(0);
    expect(out.plans).toEqual([]);
    expect(out.trial_days_left).toBe(0);
  });

  it("falls back to the permissive empty state for a non-object body", () => {
    expect(parse(null)).toEqual(EMPTY_WORKSPACE_SUBSCRIPTION);
  });
});
