import { describe, expect, it } from "vitest";
import { DesignContextStateSchema, EMPTY_DESIGN_CONTEXT_STATE } from "./context";

describe("DesignContextStateSchema", () => {
  it("fails closed to an explicit empty state at the API boundary", () => {
    expect(DesignContextStateSchema.safeParse({ active: "bad" }).success).toBe(false);
    expect(EMPTY_DESIGN_CONTEXT_STATE).toEqual({ active: null, proposal: null, history: [] });
  });

  it("accepts future response fields and defaults optional history", () => {
    const result = DesignContextStateSchema.safeParse({ active: null, proposal: null, future: true });
    expect(result.success).toBe(true);
    if (result.success) expect(result.data.history).toEqual([]);
  });
});
