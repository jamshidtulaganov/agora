import { describe, expect, it } from "vitest";
import { parseWithFallback } from "../api/schema";
import {
  EMPTY_IMPORT_CONNECTIONS,
  EMPTY_IMPORT_DRY_RUN,
  EMPTY_IMPORT_JOB,
  EMPTY_IMPORT_PLAN,
  EMPTY_START_IMPORT,
  ImportConnectionsResponseSchema,
  ImportDryRunResponseSchema,
  ImportJobSchema,
  ImportPlanSchema,
  StartImportResponseSchema,
  formatImportBytes,
  importTotalOf,
  isTerminalImportStatus,
} from "./types";

// Contract tests for the importer wire schemas (CLAUDE.md "API Response
// Compatibility"). Every case feeds a malformed or drifted payload through the
// same parseWithFallback path the ApiClient uses, and asserts the UI-facing
// value DEGRADES rather than throwing — because an installed desktop build
// will meet servers this one was never compiled against.
//
// The import surface earns the strictest version of that rule: a drifted
// response here reads as "nothing to import", which is a blank preview over a
// workspace full of issues and a green request in the network tab.

const endpoint = { endpoint: "test" };

describe("ImportConnectionsResponseSchema", () => {
  it("accepts a bare array", () => {
    const parsed = parseWithFallback(
      [{ id: "c1", source: "linear", label: "Acme", probe_status: "ok" }],
      ImportConnectionsResponseSchema,
      EMPTY_IMPORT_CONNECTIONS,
      endpoint,
    );
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.id).toBe("c1");
    expect(parsed[0]?.base_url).toBe("");
  });

  it("accepts the same rows wrapped in an object", () => {
    const parsed = parseWithFallback(
      { connections: [{ id: "c1", source: "linear" }] },
      ImportConnectionsResponseSchema,
      EMPTY_IMPORT_CONNECTIONS,
      endpoint,
    );
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.label).toBe("");
  });

  it("reads a null array as empty rather than crashing", () => {
    expect(
      parseWithFallback({ connections: null }, ImportConnectionsResponseSchema, EMPTY_IMPORT_CONNECTIONS, endpoint),
    ).toEqual([]);
  });

  it("drops a row with no id instead of sinking the whole list", () => {
    const parsed = parseWithFallback(
      [{ id: "c1", source: "linear" }, { source: "linear" }],
      ImportConnectionsResponseSchema,
      EMPTY_IMPORT_CONNECTIONS,
      endpoint,
    );
    expect(parsed).toHaveLength(1);
  });

  it("falls back when the body is not a list at all", () => {
    expect(
      parseWithFallback("nope", ImportConnectionsResponseSchema, EMPTY_IMPORT_CONNECTIONS, endpoint),
    ).toEqual(EMPTY_IMPORT_CONNECTIONS);
  });
});

describe("ImportPlanSchema", () => {
  it("parses a full plan", () => {
    const parsed = parseWithFallback(
      {
        source: { kind: "linear", ref: "acme" },
        issues: { create: 240, update: 3, total: 243 },
        comments: { create: 1180, update: 0, total: 1180 },
        containers: [{ external_id: "t1", key: "ENG", name: "Engineering", issues: 240, action: "create_project" }],
        attachments: { count: 340, bytes: 1288490188, skipped: 2, skipped_reasons: ["over the per-file cap of 25 MB"] },
        unmatched_users: 2,
        users: [{ external_id: "u1", name: "Dana Wu", email: "dana@acme.io", via: "import_identity" }],
        exact: true,
        truncated: { issues: 12 },
        warnings: ["1 private team could not be read"],
      },
      ImportPlanSchema,
      EMPTY_IMPORT_PLAN,
      endpoint,
    );
    expect(parsed.source_kind).toBe("linear");
    expect(parsed.issues.total).toBe(243);
    expect(parsed.issues.update).toBe(3);
    expect(parsed.truncated.issues).toBe(12);
    expect(parsed.attachments.skipped_reasons).toEqual(["over the per-file cap of 25 MB"]);
  });

  it("fills every missing field, including the count objects", () => {
    const parsed = parseWithFallback({}, ImportPlanSchema, EMPTY_IMPORT_PLAN, endpoint);
    expect(parsed.issues).toEqual({ create: 0, update: 0, total: 0 });
    expect(parsed.containers).toEqual([]);
    expect(parsed.warnings).toEqual([]);
    expect(parsed.attachments.skipped_reasons).toEqual([]);
  });

  it("reads a MISSING `exact` as not-exact", () => {
    // The dangerous default is the other one: claiming a count is exact when
    // the server never said so is precisely the comfortable lie §3.2 forbids.
    const parsed = parseWithFallback(
      { issues: { total: 4800, create: 4800, update: 0 } },
      ImportPlanSchema,
      EMPTY_IMPORT_PLAN,
      endpoint,
    );
    expect(parsed.exact).toBe(false);
  });

  it("survives a null array where a list was promised", () => {
    const parsed = parseWithFallback(
      { containers: null, users: null, warnings: null, unmapped_statuses: null },
      ImportPlanSchema,
      EMPTY_IMPORT_PLAN,
      endpoint,
    );
    expect(parsed.containers).toEqual([]);
    expect(parsed.users).toEqual([]);
    expect(parsed.unmapped_statuses).toEqual([]);
  });

  it("degrades one wrong-typed field rather than the whole plan", () => {
    const parsed = parseWithFallback(
      { issues: { total: "many", create: 2, update: 0 }, unmatched_users: 1 },
      ImportPlanSchema,
      EMPTY_IMPORT_PLAN,
      endpoint,
    );
    expect(parsed.issues.total).toBe(0);
    expect(parsed.issues.create).toBe(2);
    expect(parsed.unmatched_users).toBe(1);
  });
});

describe("ImportDryRunResponseSchema", () => {
  it("reads a plan wrapped beside a job id", () => {
    const parsed = parseWithFallback(
      { job_id: "j1", status: "awaiting_confirm", plan: { issues: { total: 2, create: 2, update: 0 } } },
      ImportDryRunResponseSchema,
      EMPTY_IMPORT_DRY_RUN,
      endpoint,
    );
    expect(parsed.job_id).toBe("j1");
    expect(parsed.plan.issues.total).toBe(2);
  });

  it("reads a BARE plan body, which is what the endpoint contract documents", () => {
    const parsed = parseWithFallback(
      { source: { kind: "linear" }, issues: { total: 7, create: 7, update: 0 } },
      ImportDryRunResponseSchema,
      EMPTY_IMPORT_DRY_RUN,
      endpoint,
    );
    expect(parsed.job_id).toBe("");
    expect(parsed.plan.issues.total).toBe(7);
    expect(parsed.plan.source_kind).toBe("linear");
  });

  it("falls back to an empty plan when the body is not a plan at all", () => {
    expect(
      parseWithFallback(null, ImportDryRunResponseSchema, EMPTY_IMPORT_DRY_RUN, endpoint),
    ).toEqual(EMPTY_IMPORT_DRY_RUN);
  });
});

describe("ImportJobSchema", () => {
  it("parses a finished job with its receipt", () => {
    const parsed = parseWithFallback(
      {
        id: "j1",
        source: "linear",
        status: "done",
        finished_at: "2026-09-19T10:00:00Z",
        totals: { issues: { created: 240, updated: 0, skipped: 1, failed: 2 } },
        failures: [{ kind: "attachments", identifier: "ENG-1", reason: "over the per-file cap" }],
      },
      ImportJobSchema,
      EMPTY_IMPORT_JOB,
      endpoint,
    );
    expect(parsed.status).toBe("done");
    expect(parsed.totals.issues?.created).toBe(240);
    expect(parsed.failures).toHaveLength(1);
    expect(importTotalOf(parsed, "created")).toBe(240);
    expect(importTotalOf(parsed, "failed")).toBe(2);
  });

  it("reads null totals and null failures as empty", () => {
    const parsed = parseWithFallback(
      { id: "j1", status: "running", totals: null, failures: null },
      ImportJobSchema,
      EMPTY_IMPORT_JOB,
      endpoint,
    );
    expect(parsed.totals).toEqual({});
    expect(parsed.failures).toEqual([]);
    expect(importTotalOf(parsed, "created")).toBe(0);
  });

  it("keeps an unknown status verbatim and does NOT call it terminal", () => {
    const parsed = parseWithFallback(
      { id: "j1", status: "reticulating_splines" },
      ImportJobSchema,
      EMPTY_IMPORT_JOB,
      endpoint,
    );
    expect(parsed.status).toBe("reticulating_splines");
    expect(isTerminalImportStatus(parsed.status)).toBe(false);
  });

  it("falls back on a body that is not an object", () => {
    expect(parseWithFallback([], ImportJobSchema, EMPTY_IMPORT_JOB, endpoint)).toEqual(EMPTY_IMPORT_JOB);
  });
});

describe("StartImportResponseSchema", () => {
  it("accepts job_id and the id spelling", () => {
    expect(
      parseWithFallback({ job_id: "j1" }, StartImportResponseSchema, EMPTY_START_IMPORT, endpoint).job_id,
    ).toBe("j1");
    expect(
      parseWithFallback({ id: "j2" }, StartImportResponseSchema, EMPTY_START_IMPORT, endpoint).job_id,
    ).toBe("j2");
  });

  it("defaults a missing status to running rather than to finished", () => {
    expect(
      parseWithFallback({ job_id: "j1" }, StartImportResponseSchema, EMPTY_START_IMPORT, endpoint).status,
    ).toBe("running");
  });
});

describe("helpers", () => {
  it("treats only the three terminal statuses as finished", () => {
    expect(isTerminalImportStatus("done")).toBe(true);
    expect(isTerminalImportStatus("failed")).toBe(true);
    expect(isTerminalImportStatus("cancelled")).toBe(true);
    expect(isTerminalImportStatus("running")).toBe(false);
    expect(isTerminalImportStatus("")).toBe(false);
  });

  it("formats the attachment budget", () => {
    expect(formatImportBytes(0)).toBe("0 B");
    expect(formatImportBytes(512)).toBe("512 B");
    expect(formatImportBytes(1536)).toBe("1.5 KB");
    expect(formatImportBytes(1288490188)).toBe("1.2 GB");
  });
});
