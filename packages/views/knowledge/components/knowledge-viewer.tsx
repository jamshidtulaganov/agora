"use client";

import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { Download } from "lucide-react";
import { knowledgeDocOptions, type KnowledgeDoc } from "@agora/core/knowledge";
import { Button } from "@agora/ui/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@agora/ui/components/ui/sheet";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { cn } from "@agora/ui/lib/utils";
import { Markdown } from "../../common/markdown";
import { useDownloadAttachment } from "../../editor";
import { useT } from "../../i18n";
import { KnowledgeFileIcon } from "./knowledge-file-icon";
import { KnowledgeStatus, useKnowledgeDocMeta } from "./knowledge-doc-row";

interface KnowledgeViewerProps {
  wsId: string;
  /** Open document id; "" keeps the sheet closed. */
  docId: string;
  /** The list row for this document, so the header shows before the detail loads. */
  listDoc: KnowledgeDoc | null;
  /** Section ord to scroll to and highlight (from `?section=`). */
  section: number | null;
  /** Fallback target when a search hit carried a chunk id but no ord. */
  focusChunkId: string | null;
  onClose: () => void;
}

/**
 * Shows a document the way the Assistant and agents read it: its sections in
 * order, each with its heading breadcrumb and where it came from, so a bad
 * extraction is visible to the people who uploaded the file.
 */
export function KnowledgeViewer({
  wsId,
  docId,
  listDoc,
  section,
  focusChunkId,
  onClose,
}: KnowledgeViewerProps) {
  return (
    <Sheet open={docId !== ""} onOpenChange={(open) => !open && onClose()}>
      <SheetContent
        side="right"
        className="w-full gap-0 p-0 data-[side=right]:w-full data-[side=right]:sm:max-w-2xl"
      >
        {docId !== "" && (
          <ViewerBody
            wsId={wsId}
            docId={docId}
            listDoc={listDoc}
            section={section}
            focusChunkId={focusChunkId}
          />
        )}
      </SheetContent>
    </Sheet>
  );
}

function ViewerBody({
  wsId,
  docId,
  listDoc,
  section,
  focusChunkId,
}: Omit<KnowledgeViewerProps, "onClose">) {
  const { t } = useT("knowledge");
  const { data, isLoading, isError } = useQuery(knowledgeDocOptions(wsId, docId));
  const download = useDownloadAttachment();
  const scrollRef = useRef<HTMLDivElement>(null);

  // The parser falls back to an id-less document on a drifted response.
  const loaded = data && data.document.id !== "" ? data : null;
  const doc = loaded?.document ?? listDoc;
  const chunks = loaded?.chunks ?? [];
  const targetOrd =
    section ?? (focusChunkId ? chunks.find((c) => c.id === focusChunkId)?.ord ?? null : null);

  useEffect(() => {
    if (targetOrd === null || chunks.length === 0) return;
    const el = scrollRef.current?.querySelector<HTMLElement>(`[data-ord="${targetOrd}"]`);
    el?.scrollIntoView?.({ block: "start" });
  }, [targetOrd, chunks.length]);

  return (
    <>
      <SheetHeader className="gap-1 border-b pr-12">
        {doc ? <ViewerHeader doc={doc} onDownload={(id) => void download(id)} /> : <Skeleton className="h-5 w-48" />}
      </SheetHeader>

      <div ref={scrollRef} className="flex-1 overflow-y-auto px-4 py-4">
        {isLoading && !loaded ? (
          <div className="space-y-3">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-16 w-full" />
            ))}
          </div>
        ) : isError || !loaded ? (
          <p className="text-sm text-muted-foreground">{t(($) => $.viewer.load_failed)}</p>
        ) : (
          <>
            <StatusNotice doc={loaded.document} hasSections={chunks.length > 0} />
            {chunks.length > 0 && (
              <>
                <p className="mb-2 text-xs text-muted-foreground">{t(($) => $.viewer.hint)}</p>
                <div className="divide-y">
                  {chunks.map((chunk) => {
                    const active = chunk.ord === targetOrd;
                    return (
                      <section
                        key={chunk.id || chunk.ord}
                        data-ord={chunk.ord}
                        data-active={active || undefined}
                        aria-current={active ? "location" : undefined}
                        className={cn(
                          "scroll-mt-4 px-2 py-3",
                          active && "rounded-md bg-accent/50 ring-1 ring-brand/30",
                        )}
                      >
                        {(chunk.heading_path || chunk.location) && (
                          <div className="mb-1.5 flex items-baseline justify-between gap-3 text-[11px] text-muted-foreground">
                            <span className="min-w-0 truncate" title={chunk.heading_path}>
                              {chunk.heading_path}
                            </span>
                            {chunk.location && <span className="shrink-0">{chunk.location}</span>}
                          </div>
                        )}
                        <div className="prose prose-sm dark:prose-invert max-w-none overflow-x-auto [&>*:first-child]:mt-0 [&>*:last-child]:mb-0">
                          <Markdown mode="full">{chunk.body}</Markdown>
                        </div>
                      </section>
                    );
                  })}
                </div>
              </>
            )}
          </>
        )}
      </div>
    </>
  );
}

function ViewerHeader({ doc, onDownload }: { doc: KnowledgeDoc; onDownload: (attachmentId: string) => void }) {
  const { t } = useT("knowledge");
  const meta = useKnowledgeDocMeta(doc);
  const attachmentId = doc.source === "upload" ? doc.attachment_id : undefined;
  return (
    <>
      <div className="flex min-w-0 items-center gap-2">
        <KnowledgeFileIcon doc={doc} />
        <SheetTitle className="truncate">{doc.title || doc.filename}</SheetTitle>
      </div>
      {meta && <SheetDescription className="truncate text-xs">{meta}</SheetDescription>}
      <div className="flex items-center gap-3 pt-1">
        <KnowledgeStatus doc={doc} />
        {attachmentId && (
          <Button variant="outline" size="sm" onClick={() => onDownload(attachmentId)}>
            <Download className="h-3 w-3" />
            {t(($) => $.viewer.download)}
          </Button>
        )}
      </div>
    </>
  );
}

function StatusNotice({ doc, hasSections }: { doc: KnowledgeDoc; hasSections: boolean }) {
  const { t } = useT("knowledge");
  let message: string | null = null;
  switch (doc.status) {
    case "processing":
      message = t(($) => $.viewer.processing);
      break;
    case "needs_ocr":
      message = t(($) => $.viewer.needs_ocr);
      break;
    case "failed":
      message = t(($) => $.viewer.failed);
      break;
    case "ready":
    default:
      message = hasSections ? null : t(($) => $.viewer.no_sections);
  }
  if (!message) return null;
  const showError = (doc.status === "failed" || doc.status === "needs_ocr") && doc.error;
  return (
    <div className="mb-4 rounded-md border bg-muted/30 px-3 py-2 text-sm">
      <p>{message}</p>
      {showError && <p className="mt-1 text-xs text-muted-foreground">{doc.error}</p>}
    </div>
  );
}
