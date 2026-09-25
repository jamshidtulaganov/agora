"use client";

import { MoreHorizontal, Pencil, RefreshCw, Star, Trash2 } from "lucide-react";
import { formatImportBytes } from "@agora/core/imports";
import type { KnowledgeDoc } from "@agora/core/knowledge";
import { Button } from "@agora/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@agora/ui/components/ui/dropdown-menu";
import { Spinner } from "@agora/ui/components/ui/spinner";
import { Tooltip, TooltipContent, TooltipTrigger } from "@agora/ui/components/ui/tooltip";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { KnowledgeFileIcon } from "./knowledge-file-icon";

export function formatKnowledgeDate(iso: string, locale: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const options: Intl.DateTimeFormatOptions = { month: "short", day: "numeric" };
  if (date.getFullYear() !== new Date().getFullYear()) options.year = "numeric";
  return date.toLocaleDateString(locale, options);
}

/** "2.1 MB · 12 pages · 30 sections · Added by Dilnoza · Sep 25". */
export function useKnowledgeDocMeta(doc: KnowledgeDoc): string {
  const { t, i18n } = useT("knowledge");
  const parts: string[] = [];
  if (doc.source === "note") parts.push(t(($) => $.documents.note));
  if (typeof doc.size_bytes === "number" && doc.size_bytes > 0) parts.push(formatImportBytes(doc.size_bytes));
  if (typeof doc.page_count === "number" && doc.page_count > 0) {
    parts.push(t(($) => $.documents.pages, { count: doc.page_count }));
  }
  if (doc.status === "ready" && doc.chunk_count > 0) {
    parts.push(t(($) => $.documents.sections, { count: doc.chunk_count }));
  }
  const date = formatKnowledgeDate(doc.created_at, i18n.language);
  if (doc.created_by_name && date) {
    parts.push(t(($) => $.documents.added_by, { name: doc.created_by_name, date }));
  } else if (date) {
    parts.push(t(($) => $.documents.added_on, { date }));
  }
  return parts.join(" · ");
}

/** The reader's verdict on a document, as a short word (and a spinner while reading). */
export function KnowledgeStatus({ doc }: { doc: KnowledgeDoc }) {
  const { t } = useT("knowledge");
  switch (doc.status) {
    case "processing":
      return (
        <span className="flex items-center gap-1 text-xs text-muted-foreground">
          <Spinner className="size-3" aria-hidden />
          {t(($) => $.status.processing)}
        </span>
      );
    case "ready":
      return <span className="text-xs text-muted-foreground">{t(($) => $.status.ready)}</span>;
    case "needs_ocr":
      return <span className="text-xs text-warning">{t(($) => $.status.needs_ocr)}</span>;
    case "failed":
    default:
      return <span className="text-xs text-destructive">{t(($) => $.status.failed)}</span>;
  }
}

interface KnowledgeDocRowProps {
  doc: KnowledgeDoc;
  canManage: boolean;
  onOpen: () => void;
  onTogglePin: () => void;
  onRename: () => void;
  onReprocess: () => void;
  onRemove: () => void;
}

export function KnowledgeDocRow({
  doc,
  canManage,
  onOpen,
  onTogglePin,
  onRename,
  onReprocess,
  onRemove,
}: KnowledgeDocRowProps) {
  const { t } = useT("knowledge");
  const meta = useKnowledgeDocMeta(doc);
  const problem = (doc.status === "failed" || doc.status === "needs_ocr") && doc.error ? doc.error : null;
  const pinLabel = t(($) => $.documents.pin);

  return (
    // A div, not a <button>: the star and the menu inside are buttons of their
    // own, and nesting interactive elements collapses them for screen readers.
    <div
      role="button"
      tabIndex={0}
      data-testid="knowledge-doc-row"
      className="-mx-2 flex cursor-pointer items-center gap-3 rounded-md px-2 py-2.5 transition-colors hover:bg-accent/40 focus-visible:bg-accent/40 focus-visible:outline-none"
      onClick={onOpen}
      onKeyDown={(event) => {
        if (event.target !== event.currentTarget) return;
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onOpen();
        }
      }}
    >
      <KnowledgeFileIcon doc={doc} />
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm font-medium">{doc.title || doc.filename}</div>
        {meta && <p className="mt-0.5 truncate text-xs text-muted-foreground">{meta}</p>}
        {problem && (
          <p
            className={cn(
              "mt-0.5 truncate text-xs",
              doc.status === "failed" ? "text-destructive" : "text-muted-foreground",
            )}
            title={problem}
          >
            {problem}
          </p>
        )}
      </div>
      <div className="shrink-0">
        <KnowledgeStatus doc={doc} />
      </div>

      {/* Row controls must not open the viewer. */}
      <div
        className="flex shrink-0 items-center"
        onClick={(event) => event.stopPropagation()}
        onKeyDown={(event) => event.stopPropagation()}
      >
        {canManage ? (
          <Tooltip>
            <TooltipTrigger
              render={
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={pinLabel}
                  aria-pressed={doc.pinned}
                  onClick={onTogglePin}
                />
              }
            >
              <Star
                className={cn(
                  "h-3.5 w-3.5",
                  doc.pinned ? "fill-brand text-brand" : "text-muted-foreground",
                )}
              />
            </TooltipTrigger>
            <TooltipContent side="bottom">{pinLabel}</TooltipContent>
          </Tooltip>
        ) : (
          doc.pinned && (
            <span role="img" aria-label={pinLabel} title={pinLabel} className="flex size-7 items-center justify-center">
              <Star className="h-3.5 w-3.5 fill-brand text-brand" aria-hidden />
            </span>
          )
        )}

        {canManage && (
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button variant="ghost" size="icon-sm" aria-label={t(($) => $.documents.actions)}>
                  <MoreHorizontal className="h-4 w-4 text-muted-foreground" />
                </Button>
              }
            />
            <DropdownMenuContent align="end" className="w-auto">
              <DropdownMenuItem onClick={onRename}>
                <Pencil className="h-3.5 w-3.5" />
                {t(($) => $.documents.rename)}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={onReprocess} disabled={doc.status === "processing"}>
                <RefreshCw className="h-3.5 w-3.5" />
                {t(($) => $.documents.reprocess)}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem variant="destructive" onClick={onRemove}>
                <Trash2 className="h-3.5 w-3.5" />
                {t(($) => $.documents.remove)}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>
    </div>
  );
}
