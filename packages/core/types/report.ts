// --- Pinned reports -----------------------------------------------------
// Phase 2a of docs/assistant-domain-plan.md. An assistant artifact is
// USER-scoped (it may aggregate every workspace its owner belongs to), so
// publishing one to a team is a deliberate act: the owner pins the artifact
// to a project, and from then on every member of that project's workspace
// can read the artifact's CURRENT content — re-running the recipe IS the
// refresh. The pin is a separate row; the artifact stays owner-owned, and a
// pin grants read of the live body only (no revision history).

/** Minimal actor shape the reports endpoints embed (owner / pinned_by). */
export interface ReportActorRef {
  id: string;
  name: string;
}

/** One row of GET /api/projects/{id}/reports — metadata, no body. */
export interface PinnedReportSummary {
  /** Identifies the PIN, not the artifact: the read + unpin routes key on it. */
  pin_id: string;
  artifact_id: string;
  title: string;
  /** One of AssistantArtifactKind, or an unknown future kind. */
  kind: string;
  version: number;
  updated_at: string;
  created_at: string;
  pinned_by: ReportActorRef;
  owner: ReportActorRef;
  /**
   * Scheduled refresh (Phase 2b), when the pin has one. Optional in both
   * directions: a backend older than 2b omits the key entirely, a pin with no
   * schedule sends null, and a schedule this build can't read degrades to
   * null too — the cadence badge is decoration, never a reason to lose a row.
   */
  schedule?: ReportSchedule | null;
}

/** GET /api/reports/{pinId} — the same metadata plus the current body. */
export interface PinnedReport extends PinnedReportSummary {
  content: string;
}

/** POST /api/assistant/artifacts/{id}/pins — the pin that now exists. */
export interface ReportPin {
  id: string;
  artifact_id: string;
  project_id: string;
  created_at: string;
}

// --- Scheduled refresh ---------------------------------------------------
// Phase 2b of docs/assistant-domain-plan.md. A pin can carry ONE schedule:
// when it comes due the server starts an ordinary assistant run under the
// owner's identity that re-runs the recipe and saves over the same artifact,
// so the pinned view refreshes through the 2a `report:updated` path. Presets
// only — no cron — so the tightest possible cadence is once a day.

/** The cadence presets the contract accepts. */
export type ReportScheduleFrequency = "daily" | "weekdays" | "weekly";

/**
 * Outcome of the last scheduled refresh. `""` means "never ran yet";
 * `"skipped"` means the owner's session was busy at the slot (the scheduler
 * never runs two concurrent refreshes); `"running"` is the transient value the
 * scheduler writes when it claims a slot, before the run is accepted. Only
 * `"failed"` is worth surfacing — a refresh in flight is not news to a reader.
 */
export type ReportScheduleStatus = "ok" | "failed" | "skipped" | "running" | "";

export interface ReportSchedule {
  frequency: ReportScheduleFrequency;
  /** Wall-clock "HH:MM" in `timezone`. */
  time: string;
  /** 0=Sunday … 6=Saturday (JS `getDay()` numbering). Only set for weekly. */
  weekday: number | null;
  /** IANA zone the owner's browser reported when the schedule was saved. */
  timezone: string;
  enabled: boolean;
  last_run_at: string | null;
  last_status: ReportScheduleStatus;
  next_run_at: string | null;
}

/** Body of PUT .../pins/{pinId}/schedule. `weekday` only travels for weekly. */
export interface ReportScheduleInput {
  frequency: ReportScheduleFrequency;
  time: string;
  weekday?: number;
  timezone: string;
}
