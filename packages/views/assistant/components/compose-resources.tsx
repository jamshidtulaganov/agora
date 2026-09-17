"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { FolderKanban, Loader2, X } from "lucide-react";
import { api } from "@agora/core/api";
import { useAssistantStore, type AssistantComposerContext } from "@agora/core/assistant";
import { projectListOptions } from "@agora/core/projects/queries";
import { projectResourcesOptions } from "@agora/core/projects";
import type { ProjectResource } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import { FileUploadButton } from "@agora/ui/components/common/file-upload-button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@agora/ui/components/ui/dropdown-menu";
import { useT } from "../../i18n";

const MAX_FILES = 5;
const MAX_FILE_BYTES = 64 * 1024;
const MAX_TOTAL_BYTES = 128 * 1024;
const TEXT_EXTENSIONS = new Set([
  "txt", "md", "markdown", "csv", "tsv", "json", "js", "jsx", "ts", "tsx", "mjs", "cjs",
  "py", "go", "rs", "java", "rb", "sh", "sql", "html", "css", "xml", "yaml", "yml",
  "toml", "ini", "conf", "log", "c", "cc", "h", "cpp", "hpp", "cs", "php", "swift", "kt", "lua", "bash", "zsh",
]);

function readableResource(resource: ProjectResource): string {
  if (resource.label?.trim()) return resource.label;
  if (resource.resource_type === "github_repo") {
    const url = "url" in resource.resource_ref ? resource.resource_ref.url : undefined;
    return typeof url === "string" ? url : "GitHub repository";
  }
  if (resource.resource_type === "local_directory") {
    const ref = resource.resource_ref;
    const label = "label" in ref ? ref.label : undefined;
    const path = "local_path" in ref ? ref.local_path : undefined;
    if (typeof label === "string" && label.trim()) return label;
    return typeof path === "string" ? path : "Local directory";
  }
  return resource.resource_type;
}

export function AssistantComposeResources({
  sessionId,
  workspaceId,
  onSelectionChange,
  onUploadingChange,
}: {
  sessionId: string;
  workspaceId: string | null;
  onSelectionChange?: () => void;
  onUploadingChange?: (uploading: boolean) => void;
}) {
  const { t } = useT("assistant");
  const selection = useAssistantStore((s) => s.composerContextBySession[sessionId]);
  const setSelection = useAssistantStore((s) => s.setComposerContext);
  const [uploadCount, setUploadCount] = useState(0);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const effectiveSelection = selection?.workspace_id === workspaceId ? selection : null;
  const projectId = effectiveSelection?.project_id ?? null;
  const attachments = effectiveSelection?.attachments ?? [];
  const { data: projects = [] } = useQuery({
    ...projectListOptions(workspaceId ?? ""),
    enabled: !!workspaceId,
  });
  const { data: resources = [] } = useQuery({
    ...projectResourcesOptions(workspaceId ?? "", projectId ?? ""),
    enabled: !!workspaceId && !!projectId,
  });
  const project = projects.find((item) => item.id === projectId);

  useEffect(() => {
    if (selection?.workspace_id !== workspaceId) {
      setSelection(sessionId, { workspace_id: workspaceId });
    }
  }, [selection?.workspace_id, workspaceId, sessionId, setSelection]);

  useEffect(() => {
    onUploadingChange?.(uploadCount > 0);
    return () => onUploadingChange?.(false);
  }, [uploadCount, onUploadingChange]);

  const update = (next: AssistantComposerContext) => {
    setSelection(sessionId, next);
    onSelectionChange?.();
  };

  const onFile = async (file: File) => {
    setUploadError(null);
    if (!workspaceId) {
      setUploadError(t(($) => $.resources.workspace_required));
      return;
    }
    const ext = file.name.split(".").at(-1)?.toLowerCase() ?? "";
    if (!TEXT_EXTENSIONS.has(ext) && !file.type.startsWith("text/")) {
      setUploadError(t(($) => $.resources.unsupported_type));
      return;
    }
    if (file.size > MAX_FILE_BYTES) {
      setUploadError(t(($) => $.resources.file_too_large));
      return;
    }
    if (attachments.length + uploadCount >= MAX_FILES) {
      setUploadError(t(($) => $.resources.too_many_files));
      return;
    }
    if (attachments.reduce((sum, item) => sum + item.size_bytes, 0) + file.size > MAX_TOTAL_BYTES) {
      setUploadError(t(($) => $.resources.total_too_large));
      return;
    }
    setUploadCount((count) => count + 1);
    try {
      const uploaded = await api.uploadFile(file, { workspaceId });
      if (!uploaded.id || uploaded.workspace_id !== workspaceId) throw new Error("Invalid attachment scope");
      const latest = useAssistantStore.getState().composerContextBySession[sessionId];
      // A workspace switch or removal while upload was pending must not
      // reattach a file to the wrong scope.
      if (latest?.workspace_id !== workspaceId) return;
      setSelection(sessionId, {
        ...latest,
        attachments: [
          ...(latest.attachments ?? []),
          { id: uploaded.id, filename: uploaded.filename, size_bytes: uploaded.size_bytes },
        ],
      });
      onSelectionChange?.();
    } catch {
      setUploadError(t(($) => $.resources.upload_failed));
    } finally {
      setUploadCount((count) => count - 1);
    }
  };

  return (
    <div className="mx-auto w-full max-w-2xl space-y-1.5 text-xs text-muted-foreground">
      <div className="flex flex-wrap items-center gap-1.5">
        <DropdownMenu>
          <DropdownMenuTrigger
            disabled={!workspaceId}
            className="inline-flex h-7 items-center gap-1 rounded-md border border-input px-2 hover:bg-accent disabled:opacity-50"
          >
            <FolderKanban className="size-3.5" />
            {project?.title ?? t(($) => $.resources.choose_project)}
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="max-h-64 w-60 overflow-y-auto">
            {projects.map((item) => (
              <DropdownMenuItem key={item.id} onClick={() => update({ workspace_id: workspaceId, project_id: item.id, attachments })}>
                <span className="truncate">{item.title}</span>
              </DropdownMenuItem>
            ))}
            {projects.length === 0 && <div className="px-2 py-1.5">{t(($) => $.resources.no_projects)}</div>}
          </DropdownMenuContent>
        </DropdownMenu>
        {projectId && (
          <Button type="button" size="icon-sm" variant="ghost" aria-label={t(($) => $.resources.remove_project)} onClick={() => update({ workspace_id: workspaceId, attachments })}>
            <X className="size-3.5" />
          </Button>
        )}
        <FileUploadButton size="sm" disabled={!workspaceId || uploadCount > 0} onSelect={(file) => void onFile(file)} />
        {uploadCount > 0 && <span role="status" className="inline-flex items-center gap-1"><Loader2 className="size-3 animate-spin" />{t(($) => $.resources.uploading)}</span>}
        {attachments.map((attachment) => (
          <span key={attachment.id} className="inline-flex h-7 max-w-full items-center gap-1 rounded-md bg-muted px-2 text-foreground">
            <span className="max-w-40 truncate">{attachment.filename}</span>
            <button type="button" aria-label={t(($) => $.resources.remove_file, { filename: attachment.filename })} onClick={() => update({ workspace_id: workspaceId, project_id: projectId, attachments: attachments.filter((item) => item.id !== attachment.id) })}>
              <X className="size-3" />
            </button>
          </span>
        ))}
      </div>
      {project && (
        <div aria-label={t(($) => $.resources.inventory_label, { project: project.title })}>
          {t(($) => $.resources.inventory)}: {resources.length > 0 ? resources.map(readableResource).join(", ") : t(($) => $.resources.no_resources)}
          <span className="ml-1">{t(($) => $.resources.inventory_note)}</span>
        </div>
      )}
      <p>{t(($) => $.resources.file_hint)}</p>
      {uploadError && <p role="alert" className="text-destructive">{uploadError}</p>}
    </div>
  );
}
