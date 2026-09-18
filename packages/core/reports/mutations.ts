import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { reportKeys } from "./queries";
import { assistantKeys } from "../assistant/queries";
import { createLogger } from "../logger";
import type { ReportPin } from "../types";

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
