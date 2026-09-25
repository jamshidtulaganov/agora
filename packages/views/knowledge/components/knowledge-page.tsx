"use client";

import { useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { BookOpen, Plus, StickyNote } from "lucide-react";
import { toast } from "sonner";
import { useAuthStore } from "@agora/core/auth";
import { useWorkspaceId } from "@agora/core/hooks";
import {
  KNOWLEDGE_ACCEPT,
  KNOWLEDGE_DOC_PARAM,
  KNOWLEDGE_SECTION_PARAM,
  knowledgeListOptions,
  knowledgeViewerHref,
  parseKnowledgeSection,
  useDeleteKnowledgeDoc,
  useReprocessKnowledgeDoc,
  useUpdateKnowledgeDoc,
  type KnowledgeDoc,
  type KnowledgeSearchResult,
} from "@agora/core/knowledge";
import { useCurrentWorkspace, useWorkspacePaths } from "@agora/core/paths";
import { memberListOptions } from "@agora/core/workspace/queries";
import { Button } from "@agora/ui/components/ui/button";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { FileDropOverlay, useFileDropZone } from "../../editor";
import { PageHeader } from "../../layout/page-header";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";
import { InstructionsCard } from "./instructions-card";
import { KnowledgeDocRow } from "./knowledge-doc-row";
import { NewNoteDialog, RemoveDocDialog, RenameDocDialog } from "./knowledge-dialogs";
import {
  KnowledgeSearchInput,
  KnowledgeSearchResults,
  SEARCH_DEBOUNCE_MS,
  isKnowledgeSearchActive,
  useDebouncedValue,
} from "./knowledge-search";
import { KnowledgeViewer } from "./knowledge-viewer";
import { useKnowledgeUpload } from "./use-knowledge-upload";

/**
 * Owners and admins add, pin and remove documents. The server says so in
 * `can_manage`; the caller's membership role is a second signal, so a drifted
 * response can't hide the controls from the people who need them (the server
 * still answers 403 to anyone else).
 */
function useCanManageKnowledge(wsId: string, fromServer: boolean | undefined): boolean {
  const userId = useAuthStore((s) => s.user?.id);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const role = members.find((m) => m.user_id === userId)?.role;
  return fromServer === true || role === "owner" || role === "admin";
}

export function KnowledgePage() {
  const { t } = useT("knowledge");
  const wsId = useWorkspaceId();
  const workspace = useCurrentWorkspace();
  const wsPaths = useWorkspacePaths();
  const navigation = useNavigation();

  const { data, isLoading, isError } = useQuery(knowledgeListOptions(wsId));
  const documents = data?.documents ?? [];
  const canManage = useCanManageKnowledge(wsId, data?.can_manage);

  const updateDoc = useUpdateKnowledgeDoc(wsId);
  const deleteDoc = useDeleteKnowledgeDoc(wsId);
  const reprocessDoc = useReprocessKnowledgeDoc(wsId);
  const { addFiles } = useKnowledgeUpload(wsId);

  const fileInputRef = useRef<HTMLInputElement>(null);
  const [noteOpen, setNoteOpen] = useState(false);
  const [renaming, setRenaming] = useState<KnowledgeDoc | null>(null);
  const [removing, setRemoving] = useState<KnowledgeDoc | null>(null);
  const [query, setQuery] = useState("");
  const debouncedQuery = useDebouncedValue(query, SEARCH_DEBOUNCE_MS);
  const searching = isKnowledgeSearchActive(query) && isKnowledgeSearchActive(debouncedQuery);
  // A search hit that came without an ord is located by its chunk id instead.
  const [focusChunkId, setFocusChunkId] = useState<string | null>(null);

  const { isDragOver, dropZoneProps } = useFileDropZone({
    onDrop: (files) => void addFiles(files),
    enabled: canManage,
  });

  // The viewer is URL state (`?doc=<id>&section=<ord>`) so an Assistant
  // citation can deep-link straight to a section.
  const openDocId = navigation.searchParams.get(KNOWLEDGE_DOC_PARAM) ?? "";
  const openSection = parseKnowledgeSection(navigation.searchParams.get(KNOWLEDGE_SECTION_PARAM));
  const openDoc = (id: string, section?: number | null, chunkId?: string | null) => {
    setFocusChunkId(chunkId ?? null);
    navigation.replace(knowledgeViewerHref(wsPaths.knowledge(), id, section));
  };
  const closeDoc = () => {
    setFocusChunkId(null);
    navigation.replace(wsPaths.knowledge());
  };
  const openSearchResult = (result: KnowledgeSearchResult) =>
    openDoc(result.doc_id, result.section, result.section === undefined ? result.chunk_id : null);

  const togglePin = (doc: KnowledgeDoc) =>
    updateDoc.mutate(
      { id: doc.id, pinned: !doc.pinned },
      { onError: () => toast.error(t(($) => $.documents.update_failed)) },
    );
  const rename = (doc: KnowledgeDoc, title: string) => {
    setRenaming(null);
    updateDoc.mutate(
      { id: doc.id, title },
      { onError: () => toast.error(t(($) => $.documents.update_failed)) },
    );
  };
  const reprocess = (doc: KnowledgeDoc) =>
    reprocessDoc.mutate(doc.id, {
      onSuccess: () => toast.success(t(($) => $.documents.reprocess_started, { title: doc.title })),
      onError: (err) =>
        toast.error(t(($) => $.documents.reprocess_failed), {
          description: err instanceof Error ? err.message : undefined,
        }),
    });
  const remove = (doc: KnowledgeDoc) => {
    setRemoving(null);
    if (doc.id === openDocId) closeDoc();
    deleteDoc.mutate(doc.id, {
      onSuccess: () => toast.success(t(($) => $.documents.removed, { title: doc.title })),
      onError: () => toast.error(t(($) => $.documents.remove_failed)),
    });
  };

  const pickFiles = () => fileInputRef.current?.click();

  return (
    <div className="relative flex h-full flex-col" {...dropZoneProps}>
      <PageHeader className="justify-between px-5">
        <div className="flex items-center gap-2">
          <BookOpen className="h-4 w-4 text-muted-foreground" aria-hidden />
          <h1 className="text-sm font-medium">{t(($) => $.page.title)}</h1>
          {documents.length > 0 && (
            <span className="text-xs tabular-nums text-muted-foreground">{documents.length}</span>
          )}
        </div>
        {canManage && (
          <div className="flex items-center gap-2">
            <Button size="sm" variant="ghost" onClick={() => setNoteOpen(true)}>
              <StickyNote className="h-3.5 w-3.5" />
              {t(($) => $.page.new_note)}
            </Button>
            <Button size="sm" variant="outline" onClick={pickFiles}>
              <Plus className="h-3.5 w-3.5" />
              {t(($) => $.page.add_files)}
            </Button>
          </div>
        )}
      </PageHeader>

      <div className="flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-3xl space-y-6 px-5 py-6">
          <p className="text-sm text-muted-foreground">{t(($) => $.page.subtitle)}</p>

          {workspace && <InstructionsCard workspace={workspace} canManage={canManage} />}

          <section aria-labelledby="knowledge-documents-title" className="space-y-3">
            <h2 id="knowledge-documents-title" className="text-sm font-semibold">
              {t(($) => $.documents.title)}
            </h2>

            {documents.length > 0 && <KnowledgeSearchInput value={query} onChange={setQuery} />}

            {searching ? (
              <KnowledgeSearchResults wsId={wsId} query={debouncedQuery} onOpen={openSearchResult} />
            ) : isLoading ? (
              <div className="space-y-2">
                {Array.from({ length: 3 }).map((_, i) => (
                  <Skeleton key={i} className="h-12 w-full" />
                ))}
              </div>
            ) : isError ? (
              <p className="py-6 text-center text-sm text-muted-foreground">{t(($) => $.page.load_failed)}</p>
            ) : documents.length === 0 ? (
              <div className="flex flex-col items-center rounded-lg border border-dashed px-5 py-12 text-center">
                <BookOpen className="mb-3 h-8 w-8 text-muted-foreground opacity-40" aria-hidden />
                <p className="max-w-md text-sm text-muted-foreground">
                  {canManage ? t(($) => $.documents.empty) : t(($) => $.documents.empty_member)}
                </p>
                {canManage && (
                  <Button size="sm" className="mt-4" onClick={pickFiles}>
                    <Plus className="h-3.5 w-3.5" />
                    {t(($) => $.page.add_files)}
                  </Button>
                )}
              </div>
            ) : (
              <div>
                {documents.map((doc) => (
                  <KnowledgeDocRow
                    key={doc.id}
                    doc={doc}
                    canManage={canManage}
                    onOpen={() => openDoc(doc.id)}
                    onTogglePin={() => togglePin(doc)}
                    onRename={() => setRenaming(doc)}
                    onReprocess={() => reprocess(doc)}
                    onRemove={() => setRemoving(doc)}
                  />
                ))}
              </div>
            )}
          </section>
        </div>
      </div>

      {isDragOver && <FileDropOverlay />}

      {canManage && (
        <input
          ref={fileInputRef}
          type="file"
          multiple
          accept={KNOWLEDGE_ACCEPT}
          className="hidden"
          data-testid="knowledge-file-input"
          onChange={(e) => {
            const files = Array.from(e.target.files ?? []);
            // Reset so picking the same file again still fires onChange.
            e.target.value = "";
            if (files.length > 0) void addFiles(files);
          }}
        />
      )}

      <KnowledgeViewer
        wsId={wsId}
        docId={openDocId}
        listDoc={documents.find((d) => d.id === openDocId) ?? null}
        section={openSection}
        focusChunkId={focusChunkId}
        onClose={closeDoc}
      />
      <NewNoteDialog wsId={wsId} open={noteOpen} onOpenChange={setNoteOpen} />
      <RenameDocDialog
        doc={renaming}
        onOpenChange={(open) => !open && setRenaming(null)}
        onRename={rename}
      />
      <RemoveDocDialog
        doc={removing}
        onOpenChange={(open) => !open && setRemoving(null)}
        onConfirm={remove}
      />
    </div>
  );
}
