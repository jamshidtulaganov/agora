import type { QueryClient } from "@tanstack/react-query";
import { reportKeys } from "./queries";
import type { ReportPinEventPayload } from "../types";

/**
 * `report:pinned` / `report:unpinned` / `report:updated` /
 * `report:schedule_changed` — all four move the same two caches, so they share
 * one updater.
 *
 * Workspace-scoped (unlike the assistant's own events, which are user-scoped),
 * so the caller supplies the current wsId. Per the WS rule in CLAUDE.md this
 * only invalidates; it never writes the row into the cache itself.
 *
 * `report:updated` fires when the owner re-runs a recipe and the artifact's
 * body changes behind an unchanged pin — the list moves (version /
 * updated_at) and so does any open viewer, hence both keys.
 * `report:schedule_changed` (Phase 2b) fires on PUT/DELETE of a pin's
 * schedule: nothing about the body moved, but the cadence badge on the row
 * did, and that badge is rendered from the same list query.
 */
export function onReportChanged(
  qc: QueryClient,
  wsId: string,
  payload: ReportPinEventPayload,
): void {
  if (!wsId) return;
  if (payload?.project_id) {
    qc.invalidateQueries({ queryKey: reportKeys.project(wsId, payload.project_id) });
  } else {
    // A payload without a project (drift, or a future workspace-level pin)
    // still has to refresh something — fall back to every report query in
    // this workspace rather than silently doing nothing.
    qc.invalidateQueries({ queryKey: reportKeys.all(wsId) });
  }
  if (payload?.pin_id) {
    qc.invalidateQueries({ queryKey: reportKeys.detail(wsId, payload.pin_id) });
  }
}
