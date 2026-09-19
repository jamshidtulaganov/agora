// Importer wire types (docs/importers-plan.md §3, §6).
//
// These mirror the backend's `import_connection` / `import_job` rows and the
// `ImportPlan` that server/internal/imports/plan.go produces. Two rules shape
// every schema below, and both come from CLAUDE.md "API Response
// Compatibility":
//
//   - PARSE, DON'T CAST. The desktop app installed on someone's machine is
//     older than the server it talks to, and an import response is one of the
//     widest shapes in the product. Every schema is `.loose()` with per-field
//     defaults, so a field the server grew — or dropped — degrades one value
//     instead of white-screening the page.
//   - NOTHING HERE CARRIES A SECRET. The token travels ONE way, in
//     CreateImportConnectionRequest, and never comes back: the listing query
//     on the server does not even select the sealed column. There is
//     deliberately no `secret` field on ImportConnection to tempt a renderer.

import { z } from "zod";

/** Sources the importer can read. Phase 1 ships Linear only. */
export const IMPORT_SOURCES = ["linear"] as const;
export type ImportSource = (typeof IMPORT_SOURCES)[number];

/** Probe verdicts (`import_connection.probe_status`). An empty string means
 * "never checked", which is different from "broken" and is rendered as such. */
export type ImportProbeStatus = "ok" | "invalid" | "unreachable" | "";

/** One stored source connection. Metadata only — never the key. */
export interface ImportConnection {
  id: string;
  source: string;
  label: string;
  base_url: string;
  probe_status: string;
  probed_at: string;
  created_at: string;
}

const ImportConnectionSchema = z
  .object({
    id: z.string().catch(""),
    source: z.string().catch(""),
    label: z.string().catch(""),
    base_url: z.string().catch(""),
    probe_status: z.string().catch(""),
    probed_at: z.string().catch(""),
    created_at: z.string().catch(""),
  })
  .loose();

/**
 * The connection listing accepts BOTH shapes a server might send — a bare
 * array, or an object with a `connections` key — and normalizes to an array.
 *
 * This is not indecision: the endpoint is being built in a parallel PR, the
 * two shapes are equally idiomatic in this codebase (`listReleaseIntegrations`
 * returns an array, `telegramInstallations` returns an object), and a client
 * that accepts one and blanks the page on the other is the "empty list but the
 * API returned data" bug we already have a memory note about. A row without an
 * id is unusable and is filtered out rather than sinking the whole list.
 */
export const ImportConnectionsResponseSchema = z
  .union([
    z.array(ImportConnectionSchema).nullish(),
    z.object({ connections: z.array(ImportConnectionSchema).nullish() }).loose(),
  ])
  .transform((value) => {
    const rows = Array.isArray(value) ? value : (value?.connections ?? []);
    return (rows ?? []).filter((row) => row.id !== "");
  });

export const EMPTY_IMPORT_CONNECTIONS: ImportConnection[] = [];

/** Body of `POST /api/workspaces/{id}/import/connections`. The only place a
 * source API key appears anywhere in the frontend: it is submitted once and
 * never read back. */
export interface CreateImportConnectionRequest {
  source: string;
  label: string;
  secret: string;
  base_url?: string;
}

/** What the operator chose to import (`import_job.scope`). */
export interface ImportScope {
  containers?: string[];
  since?: string;
  include_archived?: boolean;
  include_comments?: boolean;
  include_attachments?: boolean;
  provision_users?: boolean;
  max_issues?: number;
}

/** The create/update split for one entity kind. `update` is the number that
 * makes a re-import legible: 240 updates is not 240 duplicates. */
export interface ImportCountPlan {
  create: number;
  update: number;
  total: number;
}

export interface ImportContainerPlan {
  external_id: string;
  key: string;
  name: string;
  issues: number;
  project_id: string;
  /** create_project | link_project. Unknown values render generically. */
  action: string;
}

export interface ImportAttachmentPlan {
  count: number;
  bytes: number;
  skipped: number;
  skipped_reasons: string[];
  unsupported: boolean;
}

export interface ImportRelationPlan {
  total: number;
  degraded: number;
  dangling: number;
}

export interface ImportStatusMapping {
  source_name: string;
  source_category: string;
  status: string;
  via: string;
}

export interface ImportUnmappedStatus {
  name: string;
  category: string;
  issues: number;
}

/** One source user and how the server proposes to resolve them. `via` is the
 * answer to "why did it pick that", which is the first question an operator
 * asks about a name they do not recognise. */
export interface ImportUserPlan {
  external_id: string;
  name: string;
  email: string;
  via: string;
  user_id: string;
  problem: string;
}

/** The dry run's product: what WOULD happen, with everything it could not see
 * counted and named. */
export interface ImportPlan {
  source_kind: string;
  generated_at: string;
  containers: ImportContainerPlan[];
  iterations: number;
  issues: ImportCountPlan;
  comments: ImportCountPlan;
  attachments: ImportAttachmentPlan;
  relations: ImportRelationPlan;
  statuses: ImportStatusMapping[];
  unmapped_statuses: ImportUnmappedStatus[];
  users: ImportUserPlan[];
  unmatched_users: number;
  /** false when a headline count is an ESTIMATE rather than a count. The UI
   * says "about N" when this is false — the one honesty rule §4.4 makes
   * unavoidable. */
  exact: boolean;
  /** entity kind -> how many rows the credential could not reach. */
  truncated: Record<string, number>;
  warnings: string[];
}

const CountPlanSchema = z
  .object({
    create: z.number().catch(0),
    update: z.number().catch(0),
    total: z.number().catch(0),
  })
  .loose();

const EMPTY_COUNTS: ImportCountPlan = { create: 0, update: 0, total: 0 };

export const ImportPlanSchema = z
  .object({
    // `source` is an object on the wire ({kind, ref}); the UI only ever needs
    // the kind, so it is flattened here rather than threaded through every
    // component as a nested optional.
    source: z
      .object({ kind: z.string().catch(""), ref: z.string().catch("") })
      .loose()
      .nullish(),
    generated_at: z.string().nullish(),
    containers: z
      .array(
        z
          .object({
            external_id: z.string().catch(""),
            key: z.string().catch(""),
            name: z.string().catch(""),
            issues: z.number().catch(0),
            project_id: z.string().catch(""),
            action: z.string().catch(""),
          })
          .loose(),
      )
      .nullish(),
    iterations: z.number().nullish(),
    issues: CountPlanSchema.nullish(),
    comments: CountPlanSchema.nullish(),
    attachments: z
      .object({
        count: z.number().catch(0),
        bytes: z.number().catch(0),
        skipped: z.number().catch(0),
        skipped_reasons: z.array(z.string()).nullish(),
        unsupported: z.boolean().catch(false),
      })
      .loose()
      .nullish(),
    relations: z
      .object({
        total: z.number().catch(0),
        degraded: z.number().catch(0),
        dangling: z.number().catch(0),
      })
      .loose()
      .nullish(),
    statuses: z
      .array(
        z
          .object({
            source_name: z.string().catch(""),
            source_category: z.string().catch(""),
            status: z.string().catch(""),
            via: z.string().catch(""),
          })
          .loose(),
      )
      .nullish(),
    unmapped_statuses: z
      .array(
        z
          .object({
            name: z.string().catch(""),
            category: z.string().catch(""),
            issues: z.number().catch(0),
          })
          .loose(),
      )
      .nullish(),
    users: z
      .array(
        z
          .object({
            external_id: z.string().catch(""),
            name: z.string().catch(""),
            email: z.string().catch(""),
            via: z.string().catch(""),
            user_id: z.string().catch(""),
            problem: z.string().catch(""),
          })
          .loose(),
      )
      .nullish(),
    unmatched_users: z.number().nullish(),
    // A MISSING `exact` is read as false, not true: "I could not tell whether
    // these counts are exact" and "they are exact" are different claims, and
    // only one of them is safe to put in front of a migration.
    exact: z.boolean().nullish(),
    truncated: z.record(z.string(), z.number()).nullish(),
    warnings: z.array(z.string()).nullish(),
  })
  .loose()
  .transform(
    (p): ImportPlan => ({
      source_kind: p.source?.kind ?? "",
      generated_at: p.generated_at ?? "",
      containers: p.containers ?? [],
      iterations: p.iterations ?? 0,
      issues: p.issues ?? EMPTY_COUNTS,
      comments: p.comments ?? EMPTY_COUNTS,
      attachments: {
        count: p.attachments?.count ?? 0,
        bytes: p.attachments?.bytes ?? 0,
        skipped: p.attachments?.skipped ?? 0,
        skipped_reasons: p.attachments?.skipped_reasons ?? [],
        unsupported: p.attachments?.unsupported === true,
      },
      relations: p.relations ?? { total: 0, degraded: 0, dangling: 0 },
      statuses: p.statuses ?? [],
      unmapped_statuses: p.unmapped_statuses ?? [],
      users: p.users ?? [],
      unmatched_users: p.unmatched_users ?? 0,
      exact: p.exact === true,
      truncated: p.truncated ?? {},
      warnings: p.warnings ?? [],
    }),
  );

export const EMPTY_IMPORT_PLAN: ImportPlan = {
  source_kind: "",
  generated_at: "",
  containers: [],
  iterations: 0,
  issues: EMPTY_COUNTS,
  comments: EMPTY_COUNTS,
  attachments: { count: 0, bytes: 0, skipped: 0, skipped_reasons: [], unsupported: false },
  relations: { total: 0, degraded: 0, dangling: 0 },
  statuses: [],
  unmapped_statuses: [],
  users: [],
  unmatched_users: 0,
  exact: false,
  truncated: {},
  warnings: [],
};

// --- the job ----------------------------------------------------------------

/** Job statuses (`import_job.status`). Free-form on the wire on purpose — a
 * new posture must not need a migration — so the UI switches on these with a
 * generic default branch rather than assuming the list is closed. */
export const IMPORT_JOB_STATUSES = [
  "pending",
  "dry_run",
  "awaiting_confirm",
  "running",
  "done",
  "failed",
  "cancelled",
] as const;
export type ImportJobStatus = (typeof IMPORT_JOB_STATUSES)[number];

/**
 * Is this job finished? UNKNOWN STATUSES ARE NOT TERMINAL — a value this build
 * has never heard of must not make a live job look finished, and the polling
 * loop must not stop on it. Same rule as the server's TerminalJobStatus.
 */
export function isTerminalImportStatus(status: string): boolean {
  return status === "done" || status === "failed" || status === "cancelled";
}

/** One line of the receipt, per entity kind. */
export interface ImportEntityTotals {
  created: number;
  updated: number;
  skipped: number;
  failed: number;
}

export interface ImportFailure {
  kind: string;
  identifier: string;
  reason: string;
}

export interface ImportJob {
  id: string;
  source: string;
  status: string;
  started_at: string;
  finished_at: string;
  /** entity kind -> created/updated/skipped/failed. */
  totals: Record<string, ImportEntityTotals>;
  failures: ImportFailure[];
  /** The frozen plan, present from the dry run onward. */
  plan: ImportPlan | null;
}

const EntityTotalsSchema = z
  .object({
    created: z.number().catch(0),
    updated: z.number().catch(0),
    skipped: z.number().catch(0),
    failed: z.number().catch(0),
  })
  .loose();

export const ImportJobSchema = z
  .object({
    id: z.string().catch(""),
    source: z.string().catch(""),
    status: z.string().catch(""),
    started_at: z.string().nullish(),
    finished_at: z.string().nullish(),
    totals: z.record(z.string(), EntityTotalsSchema).nullish(),
    failures: z
      .array(
        z
          .object({
            kind: z.string().catch(""),
            identifier: z.string().catch(""),
            reason: z.string().catch(""),
          })
          .loose(),
      )
      .nullish(),
    plan: ImportPlanSchema.nullish(),
  })
  .loose()
  .transform(
    (j): ImportJob => ({
      id: j.id,
      source: j.source,
      status: j.status,
      started_at: j.started_at ?? "",
      finished_at: j.finished_at ?? "",
      totals: j.totals ?? {},
      // A null array is the third malformed shape CLAUDE.md names explicitly;
      // it becomes an empty list, never a crash in `.map`.
      failures: j.failures ?? [],
      plan: j.plan ?? null,
    }),
  );

export const EMPTY_IMPORT_JOB: ImportJob = {
  id: "",
  source: "",
  status: "",
  started_at: "",
  finished_at: "",
  totals: {},
  failures: [],
  plan: null,
};

/**
 * The dry-run response.
 *
 * The endpoint is documented as returning an ImportPlan; the server also has a
 * job row for every run, and the id is what the status endpoint takes. So the
 * schema accepts a plan wrapped beside a `job_id`, a whole job row, or a bare
 * plan, and normalizes. The alternative — picking one and being wrong — is a
 * blank preview with a green request in the network tab.
 */
export interface ImportDryRunResult {
  job_id: string;
  status: string;
  plan: ImportPlan;
}

export const ImportDryRunResponseSchema = z
  .object({
    job_id: z.string().nullish(),
    id: z.string().nullish(),
    status: z.string().nullish(),
    plan: ImportPlanSchema.nullish(),
  })
  .loose()
  .transform((raw, ctx) => {
    const wrapped = raw.plan;
    if (wrapped) {
      return {
        job_id: raw.job_id ?? raw.id ?? "",
        status: raw.status ?? "",
        plan: wrapped,
      } satisfies ImportDryRunResult;
    }
    // No `plan` key: the body may BE the plan. Re-parse it as one; a body that
    // is neither is a validation failure, which parseWithFallback turns into
    // the empty plan plus a logged warning.
    const bare = ImportPlanSchema.safeParse(raw);
    if (!bare.success) {
      ctx.addIssue({ code: "custom", message: "not an import plan" });
      return z.NEVER;
    }
    return {
      job_id: raw.job_id ?? raw.id ?? "",
      status: raw.status ?? "",
      plan: bare.data,
    } satisfies ImportDryRunResult;
  });

export const EMPTY_IMPORT_DRY_RUN: ImportDryRunResult = {
  job_id: "",
  status: "",
  plan: EMPTY_IMPORT_PLAN,
};

/** Body of `POST /api/workspaces/{id}/import/dry-run`. */
export interface ImportDryRunRequest {
  connection_id: string;
  scope?: ImportScope;
}

/** Body of `POST /api/workspaces/{id}/import/jobs` — the apply. */
export interface StartImportRequest {
  connection_id: string;
  scope?: ImportScope;
  /** Frozen at the server; sent only when the UI carries local overrides. */
  mapping?: Record<string, unknown>;
}

/** 202 response of the apply endpoint. */
export interface StartImportResult {
  job_id: string;
  status: string;
}

export const StartImportResponseSchema = z
  .object({
    job_id: z.string().nullish(),
    id: z.string().nullish(),
    status: z.string().nullish(),
  })
  .loose()
  .transform(
    (raw): StartImportResult => ({
      job_id: raw.job_id ?? raw.id ?? "",
      status: raw.status ?? "running",
    }),
  );

export const EMPTY_START_IMPORT: StartImportResult = { job_id: "", status: "" };

/** Sum one entity kind's receipt across the whole job, for a headline. */
export function importTotalOf(job: ImportJob, field: keyof ImportEntityTotals): number {
  return Object.values(job.totals ?? {}).reduce(
    (sum, row) => sum + (typeof row?.[field] === "number" ? row[field] : 0),
    0,
  );
}

/** Human bytes for the attachment budget. Kept here rather than in the view so
 * the same number reads the same way everywhere. */
export function formatImportBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value >= 10 || unit === 0 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`;
}
