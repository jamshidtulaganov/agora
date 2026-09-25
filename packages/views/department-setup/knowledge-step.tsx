"use client";

import { useRef } from "react";
import { Plus } from "lucide-react";
import { KNOWLEDGE_ACCEPT, type KnowledgeDoc } from "@agora/core/knowledge";
import type { Workspace } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { InstructionsCard } from "../knowledge/components/instructions-card";
import { KnowledgeStatus } from "../knowledge/components/knowledge-doc-row";
import { KnowledgeFileIcon } from "../knowledge/components/knowledge-file-icon";
import { useKnowledgeUpload } from "../knowledge/components/use-knowledge-upload";
import { useT } from "../i18n";

/**
 * Step 1: the workspace's Instructions for AI and its documents, using the
 * Knowledge page's own pieces. Viewing, pinning and removing documents stay on
 * the Knowledge page; here it's just "add what you have".
 */
export function KnowledgeStep({
  workspace,
  documents,
  isLoading,
}: {
  workspace: Workspace;
  documents: KnowledgeDoc[];
  isLoading: boolean;
}) {
  const { t } = useT("department-setup");
  const { addFiles } = useKnowledgeUpload(workspace.id);
  const fileInputRef = useRef<HTMLInputElement>(null);

  return (
    <div className="space-y-6">
      <InstructionsCard workspace={workspace} canManage />

      <section aria-labelledby="setup-documents-title" className="space-y-2">
        <div className="flex items-center justify-between gap-3">
          <h3 id="setup-documents-title" className="text-sm font-semibold">
            {t(($) => $.knowledge.files_title)}
          </h3>
          <Button
            size="sm"
            variant="outline"
            onClick={() => fileInputRef.current?.click()}
            data-testid="setup-add-files"
          >
            <Plus className="h-3.5 w-3.5" />
            {t(($) => $.knowledge.add_files)}
          </Button>
        </div>

        {isLoading ? (
          <Skeleton className="h-10 w-full" />
        ) : documents.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t(($) => $.knowledge.files_empty)}</p>
        ) : (
          <ul className="max-h-64 divide-y overflow-y-auto" data-testid="setup-documents">
            {documents.map((doc) => (
              <li key={doc.id} className="flex items-center gap-3 py-2">
                <KnowledgeFileIcon doc={doc} />
                <span className="min-w-0 flex-1 truncate text-sm">{doc.title}</span>
                <KnowledgeStatus doc={doc} />
              </li>
            ))}
          </ul>
        )}
      </section>

      <input
        ref={fileInputRef}
        type="file"
        multiple
        accept={KNOWLEDGE_ACCEPT}
        className="hidden"
        data-testid="setup-file-input"
        onChange={(e) => {
          const files = Array.from(e.target.files ?? []);
          // Reset so picking the same file again still fires onChange.
          e.target.value = "";
          if (files.length > 0) void addFiles(files);
        }}
      />
    </div>
  );
}
