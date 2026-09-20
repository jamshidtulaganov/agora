/**
 * The project risk map — which MODULES are risky, and which path globs make
 * up each one (docs/orchestration-upgrade-plan.md §A1).
 *
 * The tier a change lands in is the pipeline's most load-bearing signal: it
 * drives the dev landing mode, the auto-merge refusal, the done gate, how
 * deep QA goes and where the visual-evidence bar sits. Until now it had no
 * write endpoint, which is what "first-class object" was missing.
 *
 * The map is a LIST of module entries rather than a tier→globs dictionary:
 * a module is the thing a person actually names in review ("billing",
 * "auth"), and it carries an owner and a note that a bare glob cannot.
 *
 * The tier VOCABULARY is server-owned — `tiers` on the response is the list
 * to offer — so nothing here hard-codes an enum, and an entry whose tier
 * this build has no copy for round-trips untouched.
 */
export interface RiskMapEntry {
  /** Human name for the area, e.g. "billing". */
  module: string;
  /** One of the server's `tiers`. Rendered verbatim. */
  tier: string;
  /** Path globs that make up the module. */
  paths: string[];
  /** Who owns it. May be "". */
  owner: string;
  /** Free-form note. May be "". */
  notes: string;
}

export interface RiskMapResponse {
  project_id: string;
  /** False when this project has never been configured. */
  configured: boolean;
  risk_map: RiskMapEntry[];
  /** What a path no entry matches is treated as. */
  default_tier: string;
  /** The tier vocabulary this server speaks. */
  tiers: string[];
  /** Server-enforced cap on entries. */
  max_entries: number;
}

/** PUT body — the whole list, replace-on-write. */
export interface UpdateRiskMapRequest {
  risk_map: RiskMapEntry[];
}

/**
 * Offered when the server sends no `tiers` (an older build, or a drifted
 * response): the vocabulary `issueRiskTier` actually resolves.
 */
export const DEFAULT_RISK_MAP_TIERS: readonly string[] = [
  "critical",
  "guarded",
  "safe",
];

/** Used when the server sends no cap, so the editor still has a stop. */
export const DEFAULT_RISK_MAP_MAX_ENTRIES = 40;
