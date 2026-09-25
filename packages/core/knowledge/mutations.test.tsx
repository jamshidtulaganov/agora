/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import { workspaceKeys } from "../workspace/queries";
import type { Workspace } from "../types";
import {
  useCreateKnowledgeDoc,
  useDeleteKnowledgeDoc,
  useReprocessKnowledgeDoc,
  useUpdateKnowledgeDoc,
  useUpdateWorkspaceInstructions,
} from "./mutations";
import { knowledgeKeys, onKnowledgeUpdated } from "./queries";
import { EMPTY_KNOWLEDGE_DOC, type KnowledgeDoc, type KnowledgeListResponse } from "./types";

const WS = "ws-1";

function doc(overrides: Partial<KnowledgeDoc> = {}): KnowledgeDoc {
  return {
    id: "doc-1",
    title: "Collections SOP",
    source: "upload",
    status: "ready",
    pinned: false,
    chunk_count: 3,
    created_at: "2026-09-25T10:00:00Z",
    updated_at: "2026-09-25T10:00:00Z",
    ...overrides,
  };
}

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function listDocs(qc: QueryClient): KnowledgeDoc[] | undefined {
  return qc.getQueryData<KnowledgeListResponse>(knowledgeKeys.list(WS))?.documents;
}

describe("knowledge mutations", () => {
  let qc: QueryClient;
  const api = {
    createKnowledgeDoc: vi.fn(),
    updateKnowledgeDoc: vi.fn(),
    deleteKnowledgeDoc: vi.fn(),
    reprocessKnowledgeDoc: vi.fn(),
    listKnowledge: vi.fn(),
    getKnowledgeDoc: vi.fn(),
    updateWorkspace: vi.fn(),
  };

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    for (const fn of Object.values(api)) fn.mockReset();
    // The settle invalidations refetch; answer them with what the cache holds.
    api.listKnowledge.mockImplementation(async () => qc.getQueryData(knowledgeKeys.list(WS)));
    setApiInstance(api as unknown as ApiClient);
    qc.setQueryData<KnowledgeListResponse>(knowledgeKeys.list(WS), {
      documents: [doc(), doc({ id: "doc-2", title: "Rates" })],
      can_manage: true,
    });
  });

  afterEach(() => {
    qc.clear();
  });

  it("flips the pin before the request settles and keeps it on success", async () => {
    const pending = deferred<KnowledgeDoc>();
    api.updateKnowledgeDoc.mockReturnValue(pending.promise);
    const { result } = renderHook(() => useUpdateKnowledgeDoc(WS), { wrapper: createWrapper(qc) });

    act(() => result.current.mutate({ id: "doc-1", pinned: true }));
    await waitFor(() => expect(listDocs(qc)?.[0]?.pinned).toBe(true));
    expect(api.updateKnowledgeDoc).toHaveBeenCalledWith("doc-1", { pinned: true });

    await act(async () => {
      pending.resolve(doc({ pinned: true }));
      await pending.promise;
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(listDocs(qc)?.[0]?.pinned).toBe(true);
  });

  it("rolls the pin back when the server refuses", async () => {
    api.updateKnowledgeDoc.mockRejectedValue(new Error("forbidden"));
    const { result } = renderHook(() => useUpdateKnowledgeDoc(WS), { wrapper: createWrapper(qc) });

    act(() => result.current.mutate({ id: "doc-1", pinned: true }));
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(listDocs(qc)?.[0]?.pinned).toBe(false);
  });

  it("renames in the open viewer too", async () => {
    qc.setQueryData(knowledgeKeys.detail(WS, "doc-1"), { document: doc(), chunks: [] });
    const pending = deferred<KnowledgeDoc>();
    api.updateKnowledgeDoc.mockReturnValue(pending.promise);
    const { result } = renderHook(() => useUpdateKnowledgeDoc(WS), { wrapper: createWrapper(qc) });

    act(() => result.current.mutate({ id: "doc-1", title: "SOP v2" }));
    await waitFor(() =>
      expect(
        qc.getQueryData<{ document: KnowledgeDoc }>(knowledgeKeys.detail(WS, "doc-1"))?.document.title,
      ).toBe("SOP v2"),
    );
    expect(listDocs(qc)?.[0]?.title).toBe("SOP v2");
    pending.resolve(doc({ title: "SOP v2" }));
  });

  it("removes the row at once and restores it on failure", async () => {
    const pending = deferred<void>();
    api.deleteKnowledgeDoc.mockReturnValue(pending.promise);
    const { result } = renderHook(() => useDeleteKnowledgeDoc(WS), { wrapper: createWrapper(qc) });

    act(() => result.current.mutate("doc-1"));
    await waitFor(() => expect(listDocs(qc)?.map((d) => d.id)).toEqual(["doc-2"]));

    await act(async () => {
      pending.reject(new Error("boom"));
      await pending.promise.catch(() => undefined);
    });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(listDocs(qc)?.map((d) => d.id)).toEqual(["doc-1", "doc-2"]);
  });

  it("shows Reading… as soon as a reprocess is asked for", async () => {
    qc.setQueryData<KnowledgeListResponse>(knowledgeKeys.list(WS), {
      documents: [doc({ status: "failed", error: "bad file" })],
      can_manage: true,
    });
    const pending = deferred<KnowledgeDoc>();
    api.reprocessKnowledgeDoc.mockReturnValue(pending.promise);
    const { result } = renderHook(() => useReprocessKnowledgeDoc(WS), { wrapper: createWrapper(qc) });

    act(() => result.current.mutate("doc-1"));
    await waitFor(() => expect(listDocs(qc)?.[0]?.status).toBe("processing"));
    expect(listDocs(qc)?.[0]?.error).toBeUndefined();
    pending.resolve(doc({ status: "processing" }));
  });

  it("adds a created document first in the list", async () => {
    api.createKnowledgeDoc.mockResolvedValue(doc({ id: "doc-3", title: "Refunds", status: "processing" }));
    const { result } = renderHook(() => useCreateKnowledgeDoc(WS), { wrapper: createWrapper(qc) });

    await act(async () => {
      await result.current.mutateAsync({ attachment_id: "att-9" });
    });
    expect(api.createKnowledgeDoc).toHaveBeenCalledWith({ attachment_id: "att-9" });
    expect(listDocs(qc)?.map((d) => d.id)).toEqual(["doc-3", "doc-1", "doc-2"]);
  });

  it("never inserts the id-less fallback doc a drifted create response parses to", async () => {
    api.createKnowledgeDoc.mockResolvedValue(EMPTY_KNOWLEDGE_DOC);
    const { result } = renderHook(() => useCreateKnowledgeDoc(WS), { wrapper: createWrapper(qc) });

    await act(async () => {
      await result.current.mutateAsync({ title: "Note", body: "text" });
    });
    expect(listDocs(qc)?.map((d) => d.id)).toEqual(["doc-1", "doc-2"]);
  });

  it("writes Instructions for AI through the workspace PATCH, optimistically", async () => {
    const workspace = { id: WS, name: "Collections", slug: "collections", context: "old" } as Workspace;
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [workspace]);
    const pending = deferred<Workspace>();
    api.updateWorkspace.mockReturnValue(pending.promise);
    const { result } = renderHook(() => useUpdateWorkspaceInstructions(), { wrapper: createWrapper(qc) });

    act(() => result.current.mutate({ workspaceId: WS, context: "Be polite." }));
    await waitFor(() =>
      expect(qc.getQueryData<Workspace[]>(workspaceKeys.list())?.[0]?.context).toBe("Be polite."),
    );
    // Only `context` is sent, so a stale form can't overwrite the name.
    expect(api.updateWorkspace).toHaveBeenCalledWith(WS, { context: "Be polite." });

    await act(async () => {
      pending.reject(new Error("nope"));
      await pending.promise.catch(() => undefined);
    });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(qc.getQueryData<Workspace[]>(workspaceKeys.list())?.[0]?.context).toBe("old");
  });
});

describe("onKnowledgeUpdated", () => {
  it("invalidates every knowledge query of the workspace, and only that workspace", () => {
    const qc = new QueryClient();
    const spy = vi.spyOn(qc, "invalidateQueries");
    onKnowledgeUpdated(qc, WS);
    expect(spy).toHaveBeenCalledWith({ queryKey: ["knowledge", WS] });
  });
});
