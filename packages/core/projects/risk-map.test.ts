import { describe, expect, it } from "vitest";
import {
  pruneRiskMapEntries,
  projectRiskMapOptions,
  riskMapDefaultTier,
  riskMapKeys,
  riskMapMaxEntries,
  riskMapTierOptions,
} from "./risk-map";
import type { RiskMapEntry, RiskMapResponse } from "../types";

function entry(over: Partial<RiskMapEntry> = {}): RiskMapEntry {
  return {
    module: "auth",
    tier: "critical",
    paths: ["server/internal/auth/**"],
    owner: "",
    notes: "",
    ...over,
  };
}

function response(over: Partial<RiskMapResponse> = {}): RiskMapResponse {
  return {
    project_id: "p-1",
    configured: true,
    risk_map: [entry()],
    default_tier: "guarded",
    tiers: ["critical", "guarded", "safe"],
    max_entries: 40,
    ...over,
  };
}

describe("riskMapTierOptions", () => {
  it("offers the vocabulary the server speaks", () => {
    expect(riskMapTierOptions(response())).toEqual(["critical", "guarded", "safe"]);
  });

  it("falls back to the resolver's own tiers when the server sent none", () => {
    expect(riskMapTierOptions(response({ tiers: [], risk_map: [] }))).toEqual([
      "critical",
      "guarded",
      "safe",
    ]);
    expect(riskMapTierOptions(undefined)).toEqual(["critical", "guarded", "safe"]);
  });

  it("always offers a tier an entry already uses, so editing cannot retier it", () => {
    const opts = riskMapTierOptions(
      response({ tiers: ["critical", "safe"], risk_map: [entry({ tier: "nuclear" })] }),
    );
    expect(opts).toEqual(["critical", "safe", "nuclear"]);
  });
});

describe("riskMapDefaultTier / riskMapMaxEntries", () => {
  it("states what an unmatched path is treated as", () => {
    expect(riskMapDefaultTier(response())).toBe("guarded");
    expect(riskMapDefaultTier(response({ default_tier: "" }))).toBe("guarded");
    expect(riskMapDefaultTier(undefined)).toBe("guarded");
  });

  it("keeps a usable cap when the server sends none", () => {
    expect(riskMapMaxEntries(response())).toBe(40);
    expect(riskMapMaxEntries(response({ max_entries: 0 }))).toBe(40);
    expect(riskMapMaxEntries(undefined)).toBe(40);
  });
});

describe("pruneRiskMapEntries", () => {
  it("trims globs and drops blank ones", () => {
    expect(pruneRiskMapEntries([entry({ paths: ["  auth/** ", "", "   "] })])).toEqual([
      entry({ paths: ["auth/**"] }),
    ]);
  });

  it("drops an entry that names nothing and matches nothing", () => {
    expect(pruneRiskMapEntries([entry({ module: "  ", paths: [] }), entry()])).toEqual([entry()]);
  });

  it("keeps a half-written entry that at least has a name", () => {
    // Visible and finishable beats silently deleted on the user's behalf.
    expect(pruneRiskMapEntries([entry({ module: "billing", paths: [] })])).toEqual([
      entry({ module: "billing", paths: [] }),
    ]);
  });

  it("round-trips a tier this build has no copy for", () => {
    expect(pruneRiskMapEntries([entry({ tier: "nuclear" })])[0]?.tier).toBe("nuclear");
  });
});

describe("projectRiskMapOptions", () => {
  it("keys under the project detail so a workspace switch swaps the entry", () => {
    expect(projectRiskMapOptions("ws-1", "p-1").queryKey).toEqual(riskMapKeys.detail("ws-1", "p-1"));
    expect(projectRiskMapOptions("ws-1", "p-1").queryKey).not.toEqual(
      projectRiskMapOptions("ws-2", "p-1").queryKey,
    );
  });

  it("stays disabled until both ids are known", () => {
    expect(projectRiskMapOptions("", "p-1").enabled).toBe(false);
    expect(projectRiskMapOptions("ws-1", "").enabled).toBe(false);
    expect(projectRiskMapOptions("ws-1", "p-1").enabled).toBe(true);
  });
});
