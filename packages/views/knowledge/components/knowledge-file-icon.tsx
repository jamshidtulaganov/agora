"use client";

import { File, FileSpreadsheet, FileText, FileType, StickyNote } from "lucide-react";
import { knowledgeFileKind, type KnowledgeDoc, type KnowledgeFileKind } from "@agora/core/knowledge";
import { cn } from "@agora/ui/lib/utils";

const ICONS: Record<KnowledgeFileKind, typeof File> = {
  pdf: FileText,
  word: FileType,
  sheet: FileSpreadsheet,
  text: File,
  note: StickyNote,
};

export function KnowledgeFileIcon({
  doc,
  className,
}: {
  doc: Pick<KnowledgeDoc, "source" | "filename" | "content_type">;
  className?: string;
}) {
  const Icon = ICONS[knowledgeFileKind(doc)];
  return <Icon className={cn("h-4 w-4 shrink-0 text-muted-foreground", className)} aria-hidden />;
}
