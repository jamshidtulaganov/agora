import { useMutation, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { workspaceKeys } from "../workspace/queries";
import type { Workspace } from "../types";
import { knowledgeKeys } from "./queries";
import type {
  CreateKnowledgeDocRequest,
  KnowledgeDoc,
  KnowledgeDocDetail,
  KnowledgeListResponse,
  UpdateKnowledgeDocRequest,
} from "./types";

// Knowledge writes. Pin, rename, remove and reprocess are optimistic — each
// flips one visible thing, and a failure rolls back and refetches. Creating a
// document is not: its row only means something once the server accepted the
// file, so it lands on success (as "processing") and the WS event carries it
// the rest of the way.

type ListSnapshot = { previous: KnowledgeListResponse | undefined };

function patchListDoc(
  qc: QueryClient,
  wsId: string,
  id: string,
  patch: (doc: KnowledgeDoc) => KnowledgeDoc,
): void {
  qc.setQueryData<KnowledgeListResponse>(knowledgeKeys.list(wsId), (old) =>
    old ? { ...old, documents: old.documents.map((d) => (d.id === id ? patch(d) : d)) } : old,
  );
}

function patchDetailDoc(
  qc: QueryClient,
  wsId: string,
  id: string,
  patch: (doc: KnowledgeDoc) => KnowledgeDoc,
): void {
  qc.setQueryData<KnowledgeDocDetail>(knowledgeKeys.detail(wsId, id), (old) =>
    old ? { ...old, document: patch(old.document) } : old,
  );
}

/** Puts a server-returned doc into the list cache (replacing, or adding first). */
function upsertListDoc(qc: QueryClient, wsId: string, doc: KnowledgeDoc): void {
  // The parser falls back to an id-less doc on a drifted response; never let
  // that phantom row into the list — the settle refetch brings the real one.
  if (!doc.id) return;
  qc.setQueryData<KnowledgeListResponse>(knowledgeKeys.list(wsId), (old) => {
    if (!old) return old;
    const exists = old.documents.some((d) => d.id === doc.id);
    return {
      ...old,
      documents: exists
        ? old.documents.map((d) => (d.id === doc.id ? doc : d))
        : [doc, ...old.documents],
    };
  });
}

export function useCreateKnowledgeDoc(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateKnowledgeDocRequest) => api.createKnowledgeDoc(req),
    onSuccess: (doc) => upsertListDoc(qc, wsId, doc),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: knowledgeKeys.list(wsId) });
    },
  });
}

/** Rename and/or pin. The pin star flips before the round-trip. */
export function useUpdateKnowledgeDoc(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...req }: { id: string } & UpdateKnowledgeDocRequest) =>
      api.updateKnowledgeDoc(id, req),
    onMutate: async ({ id, ...req }) => {
      await qc.cancelQueries({ queryKey: knowledgeKeys.list(wsId) });
      await qc.cancelQueries({ queryKey: knowledgeKeys.detail(wsId, id) });
      const previous = qc.getQueryData<KnowledgeListResponse>(knowledgeKeys.list(wsId));
      const previousDetail = qc.getQueryData<KnowledgeDocDetail>(knowledgeKeys.detail(wsId, id));
      const apply = (doc: KnowledgeDoc): KnowledgeDoc => ({
        ...doc,
        ...(req.title !== undefined ? { title: req.title } : {}),
        ...(req.pinned !== undefined ? { pinned: req.pinned } : {}),
      });
      patchListDoc(qc, wsId, id, apply);
      patchDetailDoc(qc, wsId, id, apply);
      return { previous, previousDetail, id };
    },
    onError: (_err, _vars, ctx) => {
      if (!ctx) return;
      if (ctx.previous) qc.setQueryData(knowledgeKeys.list(wsId), ctx.previous);
      if (ctx.previousDetail) qc.setQueryData(knowledgeKeys.detail(wsId, ctx.id), ctx.previousDetail);
    },
    onSuccess: (doc) => upsertListDoc(qc, wsId, doc),
    onSettled: (_data, _err, vars) => {
      void qc.invalidateQueries({ queryKey: knowledgeKeys.list(wsId) });
      void qc.invalidateQueries({ queryKey: knowledgeKeys.detail(wsId, vars.id) });
    },
  });
}

/** Remove a document: the row disappears at once, and comes back on failure. */
export function useDeleteKnowledgeDoc(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteKnowledgeDoc(id),
    onMutate: async (id): Promise<ListSnapshot> => {
      await qc.cancelQueries({ queryKey: knowledgeKeys.list(wsId) });
      const previous = qc.getQueryData<KnowledgeListResponse>(knowledgeKeys.list(wsId));
      qc.setQueryData<KnowledgeListResponse>(knowledgeKeys.list(wsId), (old) =>
        old ? { ...old, documents: old.documents.filter((d) => d.id !== id) } : old,
      );
      return { previous };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.previous) qc.setQueryData(knowledgeKeys.list(wsId), ctx.previous);
    },
    onSuccess: (_data, id) => {
      qc.removeQueries({ queryKey: knowledgeKeys.detail(wsId, id) });
    },
    onSettled: () => {
      // Search results may still quote the removed document.
      void qc.invalidateQueries({ queryKey: knowledgeKeys.all(wsId) });
    },
  });
}

/** Read the file again: the row goes back to "Reading…" straight away. */
export function useReprocessKnowledgeDoc(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.reprocessKnowledgeDoc(id),
    onMutate: async (id): Promise<ListSnapshot> => {
      await qc.cancelQueries({ queryKey: knowledgeKeys.list(wsId) });
      const previous = qc.getQueryData<KnowledgeListResponse>(knowledgeKeys.list(wsId));
      patchListDoc(qc, wsId, id, (doc) => ({ ...doc, status: "processing", error: undefined }));
      return { previous };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.previous) qc.setQueryData(knowledgeKeys.list(wsId), ctx.previous);
    },
    onSuccess: (doc) => upsertListDoc(qc, wsId, doc),
    onSettled: (_data, _err, id) => {
      void qc.invalidateQueries({ queryKey: knowledgeKeys.list(wsId) });
      void qc.invalidateQueries({ queryKey: knowledgeKeys.detail(wsId, id) });
    },
  });
}

/**
 * "Instructions for AI" — the workspace's `context` field, written through the
 * existing workspace PATCH (partial: only `context` is sent, so nothing else on
 * the workspace can be overwritten by a stale form). Optimistic on the
 * workspace list cache, which is where `useCurrentWorkspace` reads it from.
 */
export function useUpdateWorkspaceInstructions() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ workspaceId, context }: { workspaceId: string; context: string }) =>
      api.updateWorkspace(workspaceId, { context }),
    onMutate: async ({ workspaceId, context }) => {
      await qc.cancelQueries({ queryKey: workspaceKeys.list() });
      const previous = qc.getQueryData<Workspace[]>(workspaceKeys.list());
      qc.setQueryData<Workspace[]>(workspaceKeys.list(), (old) =>
        old?.map((ws) => (ws.id === workspaceId ? { ...ws, context } : ws)),
      );
      return { previous };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.previous) qc.setQueryData(workspaceKeys.list(), ctx.previous);
    },
    onSuccess: (updated, { workspaceId }) => {
      // The PATCH response is the whole workspace; only trust it when it is
      // the one we changed (a drifted body must not replace a list entry).
      if (updated?.id !== workspaceId) return;
      qc.setQueryData<Workspace[]>(workspaceKeys.list(), (old) =>
        old?.map((ws) => (ws.id === workspaceId ? updated : ws)),
      );
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: workspaceKeys.list() });
    },
  });
}
