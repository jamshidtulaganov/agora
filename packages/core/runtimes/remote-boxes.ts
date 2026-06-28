import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { ConnectedBox, CreateRemoteBoxRequest } from "../types";

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
