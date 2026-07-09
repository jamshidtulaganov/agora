import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { issueKeys } from "../issues/queries";
import type {
  ConnectedBox,
  CreateRemoteBoxRequest,
  ProvisionBoxRequest,
  ProvisionBoxResult,
} from "../types";

// Remote Boxes query/mutation hooks. The server gates the endpoints behind
// AGORA_REMOTE_BOXES_ENABLED (404 when off); the client list method falls back
// to [] on any error/contract-drift, so a disabled feature renders an empty
// list rather than erroring.

export const remoteBoxKeys = {
  all: (wsId: string) => ["remote-boxes", wsId] as const,
  list: (wsId: string) => [...remoteBoxKeys.all(wsId), "list"] as const,
};

export function remoteBoxesOptions(wsId: string) {
  return queryOptions({
    queryKey: remoteBoxKeys.list(wsId),
    queryFn: () => api.listRemoteBoxes(),
    staleTime: 30_000,
  });
}

export function useCreateRemoteBox(wsId: string) {
  const qc = useQueryClient();
  return useMutation<ConnectedBox, Error, CreateRemoteBoxRequest>({
    mutationFn: (data) => api.createRemoteBox(data),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: remoteBoxKeys.all(wsId) });
    },
  });
}

export function useDeleteRemoteBox(wsId: string) {
  const qc = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (id) => api.deleteRemoteBox(id),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: remoteBoxKeys.all(wsId) });
    },
  });
}

export function useSyncRemoteBox(wsId: string) {
  const qc = useQueryClient();
  return useMutation<
    Awaited<ReturnType<typeof api.syncRemoteBox>>,
    Error,
    { id: string; branch: string }
  >({
    mutationFn: ({ id, branch }) => api.syncRemoteBox(id, branch),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: remoteBoxKeys.all(wsId) });
    },
  });
}

// Bind/unbind a box to a project (project_id == "" unbinds). Invalidates the
// list so the bound-box badge and any deploy-qa affordance reflect it.
export function useBindRemoteBox(wsId: string) {
  const qc = useQueryClient();
  return useMutation<ConnectedBox, Error, { id: string; projectId: string }>({
    mutationFn: ({ id, projectId }) => api.bindConnectedBox(id, projectId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: remoteBoxKeys.all(wsId) });
    },
  });
}

// Provision a per-developer QA box for a member. A dry run touches nothing and
// returns the runbook for review; a real run registers a box, so invalidate the
// list onSettled to surface it (a dry run just no-ops the refetch).
export function useProvisionRemoteBox(wsId: string) {
  const qc = useQueryClient();
  return useMutation<ProvisionBoxResult, Error, ProvisionBoxRequest>({
    mutationFn: (data) => api.provisionRemoteBox(data),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: remoteBoxKeys.all(wsId) });
    },
  });
}

// Deploy an issue's branch to its project's bound QA box (git-sync). Keyed by
// issue; invalidates the box list so last_branch/status stay fresh, plus the
// issue's deploy-events query (deploy P0) so the stepper's deploySynced signal
// and the Deploy lens's history pick up the row the server just wrote.
export function useDeployIssueQA(wsId: string) {
  const qc = useQueryClient();
  return useMutation<
    Awaited<ReturnType<typeof api.deployIssueQA>>,
    Error,
    { issueId: string; branch: string }
  >({
    mutationFn: ({ issueId, branch }) => api.deployIssueQA(issueId, branch),
    onSettled: (_data, _error, variables) => {
      qc.invalidateQueries({ queryKey: remoteBoxKeys.all(wsId) });
      if (variables?.issueId) {
        qc.invalidateQueries({ queryKey: issueKeys.deployEvents(variables.issueId) });
      }
    },
  });
}
