"use client";

import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Building2, ChevronDown, FolderKanban, Loader2, UserRound, X } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { api } from "@agora/core/api";
import { useAssistantStore, type AssistantComposerContext } from "@agora/core/assistant";
import { projectListOptions } from "@agora/core/projects/queries";
import { projectResourcesOptions } from "@agora/core/projects";
import { memberListOptions, workspaceListOptions } from "@agora/core/workspace";
import type { ProjectResource } from "@agora/core/types";
import { FileUploadButton } from "@agora/ui/components/common/file-upload-button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@agora/ui/components/ui/dropdown-menu";
import { cn } from "@agora/ui/lib/utils";
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

interface ComposeResourcesOptions {
  sessionId: string;
  /** The workspace the surrounding page is on — the default target. */
  workspaceId: string | null;
  onSelectionChange?: () => void;
  onUploadingChange?: (uploading: boolean) => void;
}

/** The three places the composer draws its message context. */
export interface ComposeResources {
  /** Destination + project + person pickers and the attach button — the
   *  composer's bottom toolbar, inside the input box. */
  toolbar: ReactNode;
  /** Attached files as removable pills, above the text. Null when none. */
  attachments: ReactNode;
  /** Context notes and upload errors under the box. Null when there is
   *  nothing to say — the attachment rules live in the paperclip tooltip. */
  notices: ReactNode;
}

/**
 * The composer's message context: which workspace the next message goes to,
 * an optional project and person, and attached text files. A hook rather than
 * a component because its pieces render in three different places inside the
 * composer (see ComposeResources) while sharing one selection + upload state.
 */
export function useAssistantComposeResources({
  sessionId,
  workspaceId,
  onSelectionChange,
  onUploadingChange,
}: ComposeResourcesOptions): ComposeResources {
  const { t } = useT("assistant");
  const selection = useAssistantStore((s) => s.composerContextBySession[sessionId]);
  const setSelection = useAssistantStore((s) => s.setComposerContext);
  const [uploadCount, setUploadCount] = useState(0);
  const [uploadError, setUploadError] = useState<string | null>(null);
  // Opening the picker is what fetches the roster: most messages never attach
  // a person, and a members request on every assistant mount would be a cost
  // paid by everyone for a feature used by a few.
  const [rosterRequested, setRosterRequested] = useState(false);
  // A picked workspace outranks the page; the page is the default, not the law.
  const pinnedWorkspaceId = selection?.workspace_pinned ? selection.workspace_id : null;
  const targetWorkspaceId = pinnedWorkspaceId ?? workspaceId;
  const effectiveSelection = selection?.workspace_id === targetWorkspaceId ? selection : null;
  const projectId = effectiveSelection?.project_id ?? null;
  const member = effectiveSelection?.member ?? null;
  const attachments = effectiveSelection?.attachments ?? [];
  const { data: workspaces = [] } = useQuery(workspaceListOptions());
  const { data: projects = [] } = useQuery({
    ...projectListOptions(targetWorkspaceId ?? ""),
    enabled: !!targetWorkspaceId,
  });
  const { data: members = [] } = useQuery({
    ...memberListOptions(targetWorkspaceId ?? ""),
    enabled: !!targetWorkspaceId && rosterRequested,
  });
  const { data: resources = [] } = useQuery({
    ...projectResourcesOptions(targetWorkspaceId ?? "", projectId ?? ""),
    enabled: !!targetWorkspaceId && !!projectId,
  });
  const project = projects.find((item) => item.id === projectId);
  const targetWorkspace = workspaces.find((item) => item.id === targetWorkspaceId);

  useEffect(() => {
    // Follow the page only while the user has not pinned a workspace of their
    // own. Without the pin check this effect would undo every explicit pick on
    // the next render.
    if (selection?.workspace_pinned) return;
    if (selection?.workspace_id !== workspaceId) {
      setSelection(sessionId, { workspace_id: workspaceId });
    }
  }, [selection?.workspace_id, selection?.workspace_pinned, workspaceId, sessionId, setSelection]);

  useEffect(() => {
    onUploadingChange?.(uploadCount > 0);
    return () => onUploadingChange?.(false);
  }, [uploadCount, onUploadingChange]);

  const update = (next: AssistantComposerContext) => {
    setSelection(sessionId, next);
    onSelectionChange?.();
  };

  // The selection as it stands, so changing one chip never drops another.
  // The store clears project/member/files by itself when the workspace moves.
  const current = (): AssistantComposerContext => ({
    workspace_id: targetWorkspaceId,
    ...(pinnedWorkspaceId ? { workspace_pinned: true } : {}),
    project_id: projectId,
    member,
    attachments,
  });

  const onFile = async (file: File) => {
    setUploadError(null);
    if (!targetWorkspaceId) {
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
      const uploaded = await api.uploadFile(file, { workspaceId: targetWorkspaceId });
      if (!uploaded.id || uploaded.workspace_id !== targetWorkspaceId) throw new Error("Invalid attachment scope");
      const latest = useAssistantStore.getState().composerContextBySession[sessionId];
      // A workspace switch or removal while upload was pending must not
      // reattach a file to the wrong scope.
      if (latest?.workspace_id !== targetWorkspaceId) return;
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

  const toolbar = (
    <div className="flex min-w-0 flex-wrap items-center gap-0.5">
      <Picker
        icon={Building2}
        label={targetWorkspace?.name ?? t(($) => $.resources.choose_workspace)}
        filled={!!targetWorkspace}
        onClear={pinnedWorkspaceId ? () => update({ ...current(), workspace_id: workspaceId, workspace_pinned: false }) : undefined}
        clearLabel={t(($) => $.resources.remove_workspace)}
      >
        {workspaces.map((item) => (
          <DropdownMenuItem key={item.id} onClick={() => update({ ...current(), workspace_id: item.id, workspace_pinned: true })}>
            <span className="truncate">{item.name}</span>
          </DropdownMenuItem>
        ))}
        {workspaces.length === 0 && <div className="px-2 py-1.5 text-xs text-muted-foreground">{t(($) => $.resources.no_workspaces)}</div>}
      </Picker>
      <Picker
        icon={FolderKanban}
        label={project?.title ?? t(($) => $.resources.choose_project)}
        filled={!!project}
        disabled={!targetWorkspaceId}
        onClear={projectId ? () => update({ ...current(), project_id: null }) : undefined}
        clearLabel={t(($) => $.resources.remove_project)}
      >
        {projects.map((item) => (
          <DropdownMenuItem key={item.id} onClick={() => update({ ...current(), project_id: item.id })}>
            <span className="truncate">{item.title}</span>
          </DropdownMenuItem>
        ))}
        {projects.length === 0 && <div className="px-2 py-1.5 text-xs text-muted-foreground">{t(($) => $.resources.no_projects)}</div>}
      </Picker>
      <Picker
        icon={UserRound}
        label={member?.name ?? t(($) => $.resources.choose_member)}
        filled={!!member}
        disabled={!targetWorkspaceId}
        onOpenChange={(open) => { if (open) setRosterRequested(true); }}
        onClear={member ? () => update({ ...current(), member: null }) : undefined}
        clearLabel={t(($) => $.resources.remove_member)}
      >
        {members.map((item) => (
          <DropdownMenuItem
            key={item.user_id}
            onClick={() => update({ ...current(), member: { user_id: item.user_id, name: item.name } })}
          >
            <span className="truncate">{item.name}</span>
          </DropdownMenuItem>
        ))}
        {members.length === 0 && <div className="px-2 py-1.5 text-xs text-muted-foreground">{t(($) => $.resources.no_members)}</div>}
      </Picker>
      <FileUploadButton
        size="sm"
        className="ml-0.5 size-7 rounded-md"
        title={t(($) => $.resources.file_hint)}
        disabled={!targetWorkspaceId || uploadCount > 0}
        onSelect={(file) => void onFile(file)}
      />
      {uploadCount > 0 && (
        <span role="status" className="inline-flex items-center gap-1 px-1 text-xs text-muted-foreground">
          <Loader2 className="size-3 animate-spin" />
          {t(($) => $.resources.uploading)}
        </span>
      )}
    </div>
  );

  const attachmentPills = attachments.length > 0 ? (
    <div className="flex flex-wrap gap-1">
      {attachments.map((attachment) => (
        <span
          key={attachment.id}
          className="inline-flex h-6 max-w-full items-center gap-1 rounded-md bg-muted pl-2 pr-1 text-xs text-foreground"
        >
          <span className="max-w-48 truncate">{attachment.filename}</span>
          <button
            type="button"
            className="inline-flex size-4 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            aria-label={t(($) => $.resources.remove_file, { filename: attachment.filename })}
            onClick={() => update({ ...current(), attachments: attachments.filter((item) => item.id !== attachment.id) })}
          >
            <X className="size-3" />
          </button>
        </span>
      ))}
    </div>
  ) : null;

  const hasNotices = !!project || !!member || !!uploadError;
  const notices = hasNotices ? (
    <div className="space-y-1 text-xs text-muted-foreground">
      {project && (
        <p aria-label={t(($) => $.resources.inventory_label, { project: project.title })}>
          {t(($) => $.resources.inventory)}: {resources.length > 0 ? resources.map(readableResource).join(", ") : t(($) => $.resources.no_resources)}
          <span className="ml-1">{t(($) => $.resources.inventory_note)}</span>
        </p>
      )}
      {member && <p>{t(($) => $.resources.member_hint, { member: member.name })}</p>}
      {uploadError && <p role="alert" className="text-destructive">{uploadError}</p>}
    </div>
  ) : null;

  return { toolbar, attachments: attachmentPills, notices };
}

/**
 * One context picker in the composer toolbar: a quiet ghost button that opens
 * a menu. Empty it reads as a muted label ("Project"); once chosen it shows the
 * value on a muted fill, with an attached clear button.
 */
function Picker({
  icon: Icon,
  label,
  filled,
  disabled,
  onClear,
  clearLabel,
  onOpenChange,
  children,
}: {
  icon: LucideIcon;
  label: string;
  filled: boolean;
  disabled?: boolean;
  onClear?: () => void;
  clearLabel: string;
  onOpenChange?: (open: boolean) => void;
  children: ReactNode;
}) {
  return (
    <span className={cn("inline-flex min-w-0 items-center rounded-md", filled && "bg-muted")}>
      <DropdownMenu onOpenChange={onOpenChange}>
        <DropdownMenuTrigger
          disabled={disabled}
          className={cn(
            "inline-flex h-7 min-w-0 max-w-44 items-center gap-1 rounded-md px-2 text-xs transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50",
            filled ? "text-foreground" : "text-muted-foreground",
            onClear && "rounded-r-none pr-1",
          )}
        >
          <Icon className="size-3.5 shrink-0" />
          <span className="truncate">{label}</span>
          {!onClear && <ChevronDown className="size-3 shrink-0 opacity-60" />}
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="max-h-64 w-60 overflow-y-auto">
          {children}
        </DropdownMenuContent>
      </DropdownMenu>
      {onClear && (
        <button
          type="button"
          aria-label={clearLabel}
          onClick={onClear}
          className="inline-flex h-7 w-6 shrink-0 items-center justify-center rounded-r-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        >
          <X className="size-3" />
        </button>
      )}
    </span>
  );
}
