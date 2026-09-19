import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { importKeys } from "./queries";
import type {
  CreateImportConnectionRequest,
  ImportDryRunRequest,
  StartImportRequest,
} from "./types";

/**
 * Save (or rotate) a source connection.
 *
 * The key travels in this one request and is never read back: the response
 * carries metadata only, and the form drops the value the moment the mutation
 * settles. Invalidating on SETTLE rather than on success is deliberate — a
 * failed save may still have rotated the row (the server upserts on
 * workspace+source+label), so the list resyncs either way.
 */
export function useCreateImportConnection(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateImportConnectionRequest) => api.createImportConnection(wsId, req),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: importKeys.connections(wsId) });
    },
  });
}

/** Re-check a stored key against the source. */
export function useProbeImportConnection(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (connectionId: string) => api.probeImportConnection(wsId, connectionId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: importKeys.connections(wsId) });
    },
  });
}

/** Forget a connection. The sealed key goes with the row. */
export function useDeleteImportConnection(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (connectionId: string) => api.deleteImportConnection(wsId, connectionId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: importKeys.connections(wsId) });
    },
  });
}

/**
 * Survey the source. WRITES NOTHING into the workspace — it opens a job row
 * and produces the plan — so there is no issue/project cache to invalidate
 * here, only the job itself.
 */
export function useDryRunImport(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: ImportDryRunRequest) => api.dryRunImport(wsId, req),
    onSettled: (result) => {
      if (result?.job_id) {
        qc.invalidateQueries({ queryKey: importKeys.job(wsId, result.job_id) });
      }
    },
  });
}

/**
 * Start the import the plan described.
 *
 * NOT optimistic, and deliberately so: the default in this codebase is to
 * apply locally and roll back, but there is no local approximation of "240
 * issues appeared". The job is the truth and the poll is how it arrives. On
 * settle everything the import writes into is invalidated, because rows start
 * landing while the job runs.
 */
export function useStartImport(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: StartImportRequest) => api.startImport(wsId, req),
    onSettled: (result) => {
      if (result?.job_id) {
        qc.invalidateQueries({ queryKey: importKeys.job(wsId, result.job_id) });
      }
      qc.invalidateQueries({ queryKey: ["issues"] });
      qc.invalidateQueries({ queryKey: ["projects"] });
      qc.invalidateQueries({ queryKey: ["labels"] });
    },
  });
}

/** Ask the server to stop a running import. What it already wrote stays
 * written — a re-run is an upsert, not a repair. */
export function useCancelImport(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (jobId: string) => api.cancelImport(wsId, jobId),
    onSettled: (_data, _error, jobId) => {
      qc.invalidateQueries({ queryKey: importKeys.job(wsId, jobId) });
      qc.invalidateQueries({ queryKey: ["issues"] });
    },
  });
}
