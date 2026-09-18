import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { reportKeys } from "./queries";
import { assistantKeys } from "../assistant/queries";
import { createLogger } from "../logger";
import type { ReportPin, ReportScheduleInput } from "../types";

const logger = createLogger("reports.mut");

/**
 * Publishes an artifact to a project.
 *
 * NOT optimistic, unlike most mutations in this codebase. The rule exists so
 * the user never waits on the server for something they can see — but a pin
 * is a rare, deliberate publish act with a dialog around it, and the row it
 * creates lands on a *different* surface (the project page), not the one the
 * user is looking at. There is also nothing to render optimistically: the pin
 * id comes back from the server and is what the Unpin action addresses. So:
 * settle, then invalidate.
 */
export function usePinArtifact(wsId: string) {
  const qc = useQueryClient();

  return useMutation({
    mutationFn: ({ artifactId, projectId }: { artifactId: string; projectId: string }) => {
      logger.info("pinArtifact.start", { artifactId, projectId });
      return api.pinArtifact(artifactId, projectId);
    },
    onError: (err, vars) => {
      logger.warn("pinArtifact.error", { ...vars, err });
    },
    onSettled: (_pin: ReportPin | undefined, _err, vars) => {
      qc.invalidateQueries({ queryKey: reportKeys.project(wsId, vars.projectId) });
      // The artifact itself is unchanged by a pin, but its cached row is what
      // the pane renders beside the new Unpin affordance — keep the two reads
      // from disagreeing after a publish.
      qc.invalidateQueries({ queryKey: assistantKeys.artifact(vars.artifactId) });
    },
  });
}

/** Removes a pin. Same settle-then-invalidate posture as the pin itself. */
export function useUnpinArtifact(wsId: string) {
  const qc = useQueryClient();

  return useMutation({
    mutationFn: ({
      artifactId,
      pinId,
    }: {
      artifactId: string;
      pinId: string;
      /** Only used to target the invalidation — not sent to the server. */
      projectId: string;
    }) => {
      logger.info("unpinArtifact.start", { artifactId, pinId });
      return api.unpinArtifact(artifactId, pinId);
    },
    onError: (err, vars) => {
      logger.warn("unpinArtifact.error", { ...vars, err });
    },
    onSettled: (_data, _err, vars) => {
      qc.invalidateQueries({ queryKey: reportKeys.project(wsId, vars.projectId) });
      qc.removeQueries({ queryKey: reportKeys.detail(wsId, vars.pinId) });
      qc.invalidateQueries({ queryKey: assistantKeys.artifact(vars.artifactId) });
    },
  });
}

/**
 * Creates or replaces a pin's refresh schedule (Phase 2b).
 *
 * Same settle-then-invalidate posture as the pin mutations above, and for the
 * same reason: this is a rare, deliberate act inside a dialog, and what it
 * changes is a cadence badge on a DIFFERENT surface (the project's Reports
 * rows), not the control the user is touching. There is also nothing
 * trustworthy to render optimistically — `next_run_at` is computed server-side
 * in the schedule's timezone, so a local guess would be a lie the badge shows.
 */
export function useSetReportSchedule(wsId: string) {
  const qc = useQueryClient();

  return useMutation({
    mutationFn: ({
      artifactId,
      pinId,
      schedule,
    }: {
      artifactId: string;
      pinId: string;
      schedule: ReportScheduleInput;
      /** Only used to target the invalidation — not sent to the server. */
      projectId: string;
    }) => {
      logger.info("setReportSchedule.start", { artifactId, pinId, frequency: schedule.frequency });
      return api.setReportSchedule(artifactId, pinId, schedule);
    },
    onError: (err, vars) => {
      logger.warn("setReportSchedule.error", { artifactId: vars.artifactId, pinId: vars.pinId, err });
    },
    onSettled: (_data, _err, vars) => {
      qc.invalidateQueries({ queryKey: reportKeys.project(wsId, vars.projectId) });
      qc.invalidateQueries({ queryKey: reportKeys.detail(wsId, vars.pinId) });
    },
  });
}

/** Removes a pin's schedule — the "Off" branch of the same control. */
export function useDeleteReportSchedule(wsId: string) {
  const qc = useQueryClient();

  return useMutation({
    mutationFn: ({
      artifactId,
      pinId,
    }: {
      artifactId: string;
      pinId: string;
      /** Only used to target the invalidation — not sent to the server. */
      projectId: string;
    }) => {
      logger.info("deleteReportSchedule.start", { artifactId, pinId });
      return api.deleteReportSchedule(artifactId, pinId);
    },
    onError: (err, vars) => {
      logger.warn("deleteReportSchedule.error", { artifactId: vars.artifactId, pinId: vars.pinId, err });
    },
    onSettled: (_data, _err, vars) => {
      qc.invalidateQueries({ queryKey: reportKeys.project(wsId, vars.projectId) });
      qc.invalidateQueries({ queryKey: reportKeys.detail(wsId, vars.pinId) });
    },
  });
}
