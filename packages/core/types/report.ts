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
