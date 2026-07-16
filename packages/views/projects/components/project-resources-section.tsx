/* eslint-disable i18next/no-literal-string -- project admin panel; i18n follow-up */
"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  BookOpen,
  ChevronRight,
  FolderGit,
  FolderOpen,
  Lock,
  MonitorPlay,
  Pencil,
  Plus,
  Search,
  Server,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import {
  projectResourcesOptions,
  useCreateProjectResource,
  useDeleteProjectResource,
  useUpdateProjectResource,
} from "@agora/core/projects";
import { runtimeListOptions } from "@agora/core/runtimes";
import { useWorkspaceId } from "@agora/core/hooks";
import { api } from "@agora/core/api";
import { useCurrentWorkspace } from "@agora/core/paths";
import type {
  GithubRepoResourceRef,
  LocalDirectoryResourceRef,
  ProjectResource,
} from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@agora/ui/components/ui/popover";
import {
  Tooltip,
  TooltipTrigger,
  TooltipContent,
} from "@agora/ui/components/ui/tooltip";
import {
  approveLocalDirectory,
  isDesktopShell,
  pickDirectory,
  useLocalDaemonStatus,
  validateLocalDirectory,
  type ValidateLocalDirectoryResult,
} from "../../platform";
import { FolderBrowserDialog } from "./folder-browser-dialog";
import { useT } from "../../i18n";

// Project Resources sidebar section.
//
// Type-dispatched at the row + add-flow level. Add a new resource_type by:
//   (1) extending the server validator
//   (2) extending ProjectResourceType in @agora/core/types
//   (3) adding a render case in ResourceRow and an add-control here
function isGithubRef(r: ProjectResource): r is ProjectResource & {
  resource_ref: GithubRepoResourceRef;
} {
  return r.resource_type === "github_repo";
}

function isLocalDirectoryRef(r: ProjectResource): r is ProjectResource & {
  resource_ref: LocalDirectoryResourceRef;
} {
  return r.resource_type === "local_directory";
}

export function ProjectResourcesSection({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const workspace = useCurrentWorkspace();
  const daemonStatus = useLocalDaemonStatus();
  const [open, setOpen] = useState(true);
  const [addOpen, setAddOpen] = useState(false);
  const [repoSearch, setRepoSearch] = useState("");
  const [picking, setPicking] = useState(false);

  const { data: resources = [] } = useQuery(
    projectResourcesOptions(wsId, projectId),
  );
  // Workspace runtimes power the web daemon picker (which machine hosts the
  // folder) and the machine-name label on each local_directory row. On desktop
  // the native picker owns "this machine", so the list is only load-bearing on
  // web — but it's a cheap cached list, so query unconditionally.
  const { data: runtimes = [] } = useQuery(runtimeListOptions(wsId));
  // daemon_id → machine name, for labelling rows by host on web.
  const daemonLabelById = new Map<string, string>();
  for (const rt of runtimes) {
    if (rt.daemon_id && !daemonLabelById.has(rt.daemon_id)) {
      daemonLabelById.set(rt.daemon_id, rt.name);
    }
  }
  // Online daemons the web picker can attach a folder to: one entry per
  // daemon_id (a daemon may register several provider rows), online only,
  // preferring the local-mode row's name for the label.
  const daemonOptions: DaemonOption[] = [];
  const seenDaemonIds = new Set<string>();
  for (const rt of runtimes) {
    if (!rt.daemon_id || rt.status !== "online") continue;
    if (seenDaemonIds.has(rt.daemon_id)) continue;
    seenDaemonIds.add(rt.daemon_id);
    daemonOptions.push({
      daemonId: rt.daemon_id,
      label: rt.name,
      mode: rt.runtime_mode,
    });
  }
  // Primary repo = the first github_repo by position (the order the backend
  // hands the agent; the human reorders to choose which an agent works in).
  const githubRepos = resources.filter(isGithubRef);
  const githubRepoCount = githubRepos.length;
  const firstGithubRepoId = githubRepos[0]?.id;
  const createResource = useCreateProjectResource(wsId, projectId);
  const updateResource = useUpdateProjectResource(wsId, projectId);
  const deleteResource = useDeleteProjectResource(wsId, projectId);
  const [buildingKb, setBuildingKb] = useState(false);

  // Trigger the lead agent to study the connected repos and write the project's
  // <slug>-kb knowledge skill. Backend validates lead-is-agent + has-repo.
  const handleBuildKnowledge = async () => {
    setBuildingKb(true);
    try {
      await api.buildProjectKnowledge(projectId);
      toast.success(
        "Knowledge build started — the lead agent will study the repos.",
      );
    } catch (err) {
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : "Failed to start knowledge build",
      );
    } finally {
      setBuildingKb(false);
    }
  };

  // Desktop-only entry points. We hide (not just disable) on web so users
  // there don't see an action they can never complete — the spec calls for
  // read-only on web because the daemon-id check can't be performed in the
  // browser.
  const desktopMode = isDesktopShell();
  // Web has no native folder picker and no "this machine" daemon, so it drives
  // attachment through the daemon picker instead: the human names the host and
  // types the absolute path, and the daemon validates + approves it at task
  // time (`agora daemon allow-dir` / the desktop picker / the env allowlist).
  const webMode = !desktopMode;
  const localDaemonId = daemonStatus.daemonId;

  const attachedUrls = new Set(
    resources.filter(isGithubRef).map((r) => r.resource_ref.url),
  );
  // Every daemon that already has a local_directory in this project — the web
  // picker greys these out because the backend allows at most one folder per
  // (project, daemon).
  const attachedDaemonIds = new Set(
    resources
      .filter(isLocalDirectoryRef)
      .map((r) => r.resource_ref.daemon_id),
  );
  const attachedLocalPaths = new Set(
    resources
      .filter(isLocalDirectoryRef)
      .filter((r) => r.resource_ref.daemon_id === localDaemonId)
      .map((r) => r.resource_ref.local_path),
  );
  // Per (project, daemon) we allow at most one local_directory — the
  // daemon-side resolver picks the first match by daemon_id, so two rows
  // on the same daemon would silently route the agent into one of them.
  // The server enforces this at the API boundary; the UI mirrors the
  // restriction by hiding the "Add" affordance once a row exists for the
  // current daemon, otherwise users would only discover the limit on a
  // 409 toast.
  const hasLocalDirectoryForCurrentDaemon =
    localDaemonId !== null && attachedLocalPaths.size > 0;

  const repoQuery = repoSearch.trim().toLowerCase();
  const filteredRepos =
    workspace?.repos?.filter((repo) => repo.url.toLowerCase().includes(repoQuery)) ?? [];

  const handleAttach = async (url: string) => {
    try {
      await createResource.mutateAsync({
        resource_type: "github_repo",
        resource_ref: { url },
      });
      toast.success(t(($) => $.resources.toast_attached));
    } catch (err) {
      const msg = err instanceof Error ? err.message : t(($) => $.resources.toast_attach_failed);
      toast.error(msg);
    }
  };

  const handleAttachLocalDirectory = async () => {
    if (picking) return;
    setPicking(true);
    try {
      if (!localDaemonId || !daemonStatus.running) {
        toast.error(t(($) => $.resources.toast_local_daemon_not_running));
        return;
      }
      // Race guard: the button gates on this already, but if the picker
      // is opened while a concurrent resource-create lands the user
      // would otherwise see a 409. Surface a clearer message instead.
      if (attachedLocalPaths.size > 0) {
        toast.error(t(($) => $.resources.toast_local_daemon_already_attached));
        return;
      }
      const picked = await pickDirectory();
      if (!picked.ok) {
        if (picked.reason && picked.reason !== "cancelled") {
          toast.error(
            picked.error ?? t(($) => $.resources.toast_local_pick_failed),
          );
        }
        return;
      }
      const path = picked.path ?? "";
      const fallbackLabel = picked.basename ?? path;
      if (attachedLocalPaths.has(path)) {
        toast.error(t(($) => $.resources.toast_local_already_attached));
        return;
      }
      const validation = await validateLocalDirectory(path);
      if (!validation.ok) {
        toast.error(
          localValidationMessage(validation, {
            not_absolute: t(($) => $.resources.local_validate_not_absolute),
            not_found: t(($) => $.resources.local_validate_not_found),
            not_a_directory: t(($) => $.resources.local_validate_not_a_directory),
            not_readable: t(($) => $.resources.local_validate_not_readable),
            not_writable: t(($) => $.resources.local_validate_not_writable),
            unsupported: t(($) => $.resources.local_validate_unsupported),
            fallback: t(($) => $.resources.toast_local_pick_failed),
          }),
        );
        return;
      }
      // Picking the folder is the owner's consent gesture: record it in
      // ~/.agora/local-dirs.json so the daemon accepts tasks there. An older
      // desktop shell without the bridge ("unsupported") proceeds — the
      // daemon's fail message covers manual approval via `agora daemon
      // allow-dir`. A hard approve failure aborts: attaching a resource the
      // daemon will refuse only produces failed tasks later.
      const approval = await approveLocalDirectory(path);
      if (!approval.ok && approval.reason !== "unsupported") {
        toast.error(
          approval.reason === "protected"
            ? t(($) => $.resources.local_approve_protected)
            : (approval.error ??
                t(($) => $.resources.toast_local_approve_failed)),
        );
        return;
      }
      await createResource.mutateAsync({
        resource_type: "local_directory",
        resource_ref: {
          local_path: path,
          daemon_id: localDaemonId,
          label: fallbackLabel,
        },
      });
      toast.success(t(($) => $.resources.toast_local_attached));
      setAddOpen(false);
    } catch (err) {
      const msg =
        err instanceof Error
          ? err.message
          : t(($) => $.resources.toast_local_pick_failed);
      toast.error(msg);
    } finally {
      setPicking(false);
    }
  };

  // Web attach: the human picked a host daemon + typed an absolute path in the
  // popover. There's no browser-side validation — the server checks daemon
  // access + path shape, and the daemon checks the folder exists + is approved
  // at task time. Returns true on success so the popover can close/reset.
  const handleAttachLocalDirectoryWeb = async (v: {
    daemonId: string;
    path: string;
    access: "read" | "write";
    previewUrl: string;
  }): Promise<boolean> => {
    const path = v.path.trim();
    if (!v.daemonId || !path) return false;
    const ref: LocalDirectoryResourceRef = {
      local_path: path,
      daemon_id: v.daemonId,
      access: v.access,
    };
    // access=read is only valid with worktree isolation (the server rejects
    // read + in_place), so pin it here to match.
    if (v.access === "read") ref.isolation = "worktree";
    const preview = v.previewUrl.trim();
    if (preview) ref.preview_url = preview;
    try {
      await createResource.mutateAsync({
        resource_type: "local_directory",
        resource_ref: ref,
      });
      toast.success(t(($) => $.resources.toast_local_attached));
      return true;
    } catch (err) {
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.resources.toast_local_pick_failed),
      );
      return false;
    }
  };

  const handleRemove = async (resource: ProjectResource) => {
    try {
      await deleteResource.mutateAsync(resource.id);
      toast.success(t(($) => $.resources.toast_removed));
    } catch (err) {
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.resources.toast_remove_failed),
      );
    }
  };

  // Flip a folder between read-only and read-write. UpdateProjectResource
  // replaces resource_ref wholesale, so send the full ref. access=read is only
  // valid with worktree isolation (the server rejects read + in_place), so
  // switching to read forces isolation along with it.
  const handleToggleAccess = async (
    resource: ProjectResource & { resource_ref: LocalDirectoryResourceRef },
  ) => {
    const ref = resource.resource_ref;
    const nextAccess = (ref.access ?? "write") === "write" ? "read" : "write";
    try {
      await updateResource.mutateAsync({
        resourceId: resource.id,
        data: {
          resource_ref: {
            ...ref,
            access: nextAccess,
            isolation: nextAccess === "read" ? "worktree" : ref.isolation,
          },
        },
      });
      toast.success(t(($) => $.resources.toast_access_updated));
    } catch (err) {
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.resources.toast_access_failed),
      );
    }
  };

  // Set/clear the developer's own dev-server URL that the issue Preview surface
  // proxies to. Empty string clears it. Sends the full ref (wholesale replace).
  const handleSetPreviewUrl = async (
    resource: ProjectResource & { resource_ref: LocalDirectoryResourceRef },
    nextUrl: string,
  ) => {
    const trimmed = nextUrl.trim();
    if (trimmed === (resource.resource_ref.preview_url ?? "").trim()) return;
    try {
      const ref = { ...resource.resource_ref };
      if (trimmed) ref.preview_url = trimmed;
      else delete ref.preview_url;
      await updateResource.mutateAsync({
        resourceId: resource.id,
        data: { resource_ref: ref },
      });
      toast.success(t(($) => $.resources.toast_preview_url_updated));
    } catch (err) {
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.resources.toast_preview_url_failed),
      );
    }
  };

  const handleRenameLocalDirectory = async (
    resource: ProjectResource & { resource_ref: LocalDirectoryResourceRef },
    nextLabel: string,
  ) => {
    const trimmed = nextLabel.trim();
    const previous = resource.resource_ref.label ?? resource.label ?? "";
    if (trimmed === previous.trim()) return;
    try {
      await updateResource.mutateAsync({
        resourceId: resource.id,
        data: {
          resource_ref: {
            ...resource.resource_ref,
            label: trimmed,
          },
        },
      });
      toast.success(t(($) => $.resources.toast_local_renamed));
    } catch (err) {
      const msg =
        err instanceof Error
          ? err.message
          : t(($) => $.resources.toast_local_rename_failed);
      toast.error(msg);
    }
  };

  return (
    <div>
      <button
        type="button"
        className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors mb-2 hover:bg-accent/70 ${open ? "" : "text-muted-foreground hover:text-foreground"}`}
        onClick={() => setOpen(!open)}
      >
        {t(($) => $.resources.section_header)}
        <ChevronRight
          className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`}
        />
      </button>
      {open && (
        <div className="pl-2 space-y-1.5">
          {resources.length === 0 && (
            <div className="rounded-md border border-amber-500/30 bg-amber-500/5 px-2.5 py-2">
              <p className="text-xs text-muted-foreground">
                {t(($) => $.resources.empty)}
              </p>
            </div>
          )}
          {resources.length > 0 && (
            <div className="max-h-64 space-y-1.5 overflow-y-auto pr-1">
              {resources.map((resource) => (
                <ResourceRow
                  key={resource.id}
                  resource={resource}
                  // The first github_repo (resources are position-ordered) is the
                  // PRIMARY: where an agent opens its PR/MR when the project binds
                  // several repos. Surfacing it tells the human the order matters.
                  isPrimaryRepo={
                    isGithubRef(resource) &&
                    resource.id === firstGithubRepoId
                  }
                  hasMultipleRepos={githubRepoCount > 1}
                  localDaemonId={localDaemonId}
                  webMode={webMode}
                  daemonLabel={
                    isLocalDirectoryRef(resource)
                      ? daemonLabelById.get(resource.resource_ref.daemon_id)
                      : undefined
                  }
                  // Web edits are gated server-side (requireLocalDirectoryDaemon
                  // Access re-checks owner/admin on every update), so allow the
                  // controls optimistically and surface a 403 as a toast.
                  canEdit={desktopMode || webMode}
                  onRemove={() => handleRemove(resource)}
                  onRenameLocalDirectory={handleRenameLocalDirectory}
                  onToggleAccess={handleToggleAccess}
                  onSetPreviewUrl={handleSetPreviewUrl}
                />
              ))}
            </div>
          )}
          <Popover
            open={addOpen}
            onOpenChange={(v) => {
              setAddOpen(v);
              if (!v) setRepoSearch("");
            }}
          >
            <PopoverTrigger
              render={
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-7 px-2 text-xs text-muted-foreground hover:text-foreground"
                >
                  <Plus className="size-3" />
                  {t(($) => $.resources.add_button)}
                </Button>
              }
            />
            <PopoverContent align="start" className="w-72 p-2 space-y-2">
              <div className="text-xs font-medium text-muted-foreground">
                {t(($) => $.resources.popover_title)}
              </div>
              {workspace?.repos && workspace.repos.length > 0 && (
                <>
                  <div className="relative">
                    <Search className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
                    <input
                      type="text"
                      value={repoSearch}
                      onChange={(e) => setRepoSearch(e.target.value)}
                      aria-label={t(($) => $.resources.repos_search_placeholder)}
                      placeholder={t(($) => $.resources.repos_search_placeholder)}
                      className="h-8 w-full rounded-md border bg-transparent pl-7 pr-2 text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
                    />
                  </div>
                  <div className="max-h-48 space-y-1 overflow-y-auto">
                    {filteredRepos.length === 0 && repoQuery && (
                      <p className="py-2 text-center text-xs text-muted-foreground">
                        {t(($) => $.resources.repos_search_empty)}
                      </p>
                    )}
                    {filteredRepos.map((repo) => {
                      const isAttached = attachedUrls.has(repo.url);
                      const isDisabled = isAttached || createResource.isPending;
                      return (
                        // Use aria-disabled instead of the native `disabled` attribute so
                        // hover events still reach the tooltip trigger on attached rows
                        // (browsers suppress pointer events on disabled form controls).
                        <button
                          key={repo.url}
                          type="button"
                          aria-disabled={isDisabled}
                          onClick={async () => {
                            if (isDisabled) return;
                            await handleAttach(repo.url);
                            setAddOpen(false);
                          }}
                          className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-xs text-left hover:bg-accent transition-colors aria-disabled:opacity-50 aria-disabled:cursor-not-allowed aria-disabled:hover:bg-transparent"
                        >
                          <FolderGit className="size-3.5" />
                          <Tooltip>
                            <TooltipTrigger
                              render={
                                <span className="truncate flex-1">{repo.url}</span>
                              }
                            />
                            <TooltipContent side="top">{repo.url}</TooltipContent>
                          </Tooltip>
                          {isAttached && (
                            <span className="text-[10px] text-muted-foreground">
                              {t(($) => $.resources.attached_badge)}
                            </span>
                          )}
                        </button>
                      );
                    })}
                  </div>
                </>
              )}
              <CustomRepoForm
                onSubmit={async (url) => {
                  await handleAttach(url);
                  setAddOpen(false);
                }}
              />
            </PopoverContent>
          </Popover>
          {desktopMode && (
            <div className="flex flex-col">
              <Button
                variant="ghost"
                size="sm"
                className="h-7 justify-start px-2 text-xs text-muted-foreground hover:text-foreground"
                disabled={
                  picking ||
                  createResource.isPending ||
                  !daemonStatus.running ||
                  hasLocalDirectoryForCurrentDaemon
                }
                onClick={() => {
                  void handleAttachLocalDirectory();
                }}
              >
                <FolderOpen className="size-3" />
                {t(($) => $.resources.add_local_directory_button)}
              </Button>
              {!daemonStatus.running && (
                <p className="px-2 pt-0.5 text-[10px] text-muted-foreground">
                  {t(($) => $.resources.local_daemon_offline_hint)}
                </p>
              )}
              {daemonStatus.running && hasLocalDirectoryForCurrentDaemon && (
                <p className="px-2 pt-0.5 text-[10px] text-muted-foreground">
                  {t(($) => $.resources.local_daemon_already_attached_hint)}
                </p>
              )}
            </div>
          )}
          {webMode && (
            <AddLocalDirectoryWebPopover
              wsId={wsId}
              daemonOptions={daemonOptions}
              attachedDaemonIds={attachedDaemonIds}
              submitting={createResource.isPending}
              onSubmit={handleAttachLocalDirectoryWeb}
            />
          )}
          {resources.some((r) => r.resource_type === "github_repo") && (
            <Button
              variant="ghost"
              size="sm"
              className="h-7 w-full justify-start px-2 text-xs text-muted-foreground hover:text-foreground"
              disabled={buildingKb}
              onClick={() => void handleBuildKnowledge()}
            >
              <BookOpen className="size-3" />
              {buildingKb ? "Starting…" : "Build knowledge base"}
            </Button>
          )}
        </div>
      )}
    </div>
  );
}

interface DaemonOption {
  daemonId: string;
  label: string;
  mode: "local" | "cloud";
}

interface ResourceRowProps {
  resource: ProjectResource;
  localDaemonId: string | null;
  webMode: boolean;
  daemonLabel?: string;
  canEdit: boolean;
  isPrimaryRepo?: boolean;
  hasMultipleRepos?: boolean;
  onRemove: () => void;
  onRenameLocalDirectory: (
    resource: ProjectResource & { resource_ref: LocalDirectoryResourceRef },
    nextLabel: string,
  ) => Promise<void>;
  onToggleAccess: (
    resource: ProjectResource & { resource_ref: LocalDirectoryResourceRef },
  ) => Promise<void>;
  onSetPreviewUrl: (
    resource: ProjectResource & { resource_ref: LocalDirectoryResourceRef },
    nextUrl: string,
  ) => Promise<void>;
}

function ResourceRow({
  resource,
  localDaemonId,
  webMode,
  daemonLabel,
  canEdit,
  isPrimaryRepo,
  hasMultipleRepos,
  onRemove,
  onRenameLocalDirectory,
  onToggleAccess,
  onSetPreviewUrl,
}: ResourceRowProps) {
  const { t } = useT("projects");
  if (isGithubRef(resource)) {
    const ref = resource.resource_ref;
    return (
      <div className="flex items-center gap-2 text-xs group">
        <FolderGit className="size-3.5 text-muted-foreground shrink-0" />
        <Tooltip>
          <TooltipTrigger
            render={
              <a
                href={ref.url}
                target="_blank"
                rel="noopener noreferrer"
                className="truncate flex-1 hover:underline"
              >
                {resource.label || ref.url}
              </a>
            }
          />
          <TooltipContent side="top">{ref.url}</TooltipContent>
        </Tooltip>
        {hasMultipleRepos && isPrimaryRepo && (
          <span
            className="shrink-0 rounded bg-primary/10 px-1 py-0.5 text-[9px] font-medium text-primary"
            title="Primary repo — agents open their PR/MR here"
          >
            primary
          </span>
        )}
        <button
          type="button"
          onClick={onRemove}
          className="opacity-0 group-hover:opacity-100 transition-opacity rounded-sm p-0.5 hover:bg-accent"
          title={t(($) => $.resources.remove_tooltip)}
        >
          <Trash2 className="size-3 text-muted-foreground" />
        </button>
      </div>
    );
  }

  if (isLocalDirectoryRef(resource)) {
    return (
      <LocalDirectoryRow
        resource={resource}
        localDaemonId={localDaemonId}
        webMode={webMode}
        daemonLabel={daemonLabel}
        canEdit={canEdit}
        onRemove={onRemove}
        onRename={onRenameLocalDirectory}
        onToggleAccess={onToggleAccess}
        onSetPreviewUrl={onSetPreviewUrl}
      />
    );
  }

  return (
    <div className="flex items-center gap-2 text-xs text-muted-foreground">
      <span className="truncate flex-1">
        {resource.label || resource.resource_type}
      </span>
      <button
        type="button"
        onClick={onRemove}
        className="rounded-sm p-0.5 hover:bg-accent"
        title={t(($) => $.resources.remove_tooltip)}
      >
        <Trash2 className="size-3" />
      </button>
    </div>
  );
}

interface LocalDirectoryRowProps {
  resource: ProjectResource & { resource_ref: LocalDirectoryResourceRef };
  localDaemonId: string | null;
  webMode: boolean;
  daemonLabel?: string;
  canEdit: boolean;
  onRemove: () => void;
  onRename: (
    resource: ProjectResource & { resource_ref: LocalDirectoryResourceRef },
    nextLabel: string,
  ) => Promise<void>;
  onToggleAccess: (
    resource: ProjectResource & { resource_ref: LocalDirectoryResourceRef },
  ) => Promise<void>;
  onSetPreviewUrl: (
    resource: ProjectResource & { resource_ref: LocalDirectoryResourceRef },
    nextUrl: string,
  ) => Promise<void>;
}

function LocalDirectoryRow({
  resource,
  localDaemonId,
  webMode,
  daemonLabel,
  canEdit,
  onRemove,
  onRename,
  onToggleAccess,
  onSetPreviewUrl,
}: LocalDirectoryRowProps) {
  const { t } = useT("projects");
  const ref = resource.resource_ref;
  const readOnly = ref.access === "read";
  const display = (ref.label || resource.label || ref.local_path).trim() ||
    ref.local_path;
  const isForeignDaemon =
    localDaemonId !== null && ref.daemon_id !== localDaemonId;
  const isLocalUnknown = localDaemonId === null;
  // "disabled" in the spec sense — visual de-emphasis + no chat hint, and
  // rename is hidden on foreign / unknown-daemon rows because the label
  // belongs to the owning device. Delete stays available so the user can
  // drop a stale registration from any device.
  //
  // Web has no "this machine", so the desktop mismatch rule (any row not on the
  // local daemon) would de-emphasize every row. Instead web manages folders on
  // ANY of its daemons — the server re-checks owner/admin per edit — so nothing
  // is a mismatch here; the machine name is shown so the host stays clear.
  const mismatch = webMode ? false : isForeignDaemon || isLocalUnknown;

  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(display);
  const [editingPreview, setEditingPreview] = useState(false);
  const [previewDraft, setPreviewDraft] = useState(ref.preview_url ?? "");

  const startEdit = () => {
    setDraft(display);
    setEditing(true);
  };
  const commit = async () => {
    setEditing(false);
    await onRename(resource, draft);
  };
  const cancel = () => {
    setEditing(false);
    setDraft(display);
  };
  const commitPreview = async () => {
    setEditingPreview(false);
    await onSetPreviewUrl(resource, previewDraft);
  };

  return (
    <div
      className={`flex items-center gap-2 text-xs group ${
        mismatch ? "opacity-60" : ""
      }`}
    >
      <FolderOpen className="size-3.5 text-muted-foreground shrink-0" />
      {editing ? (
        <input
          autoFocus
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={() => void commit()}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              void commit();
            } else if (e.key === "Escape") {
              e.preventDefault();
              cancel();
            }
          }}
          className="flex-1 min-w-0 rounded-sm border bg-transparent px-1 py-0.5 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring"
          aria-label={t(($) => $.resources.local_rename_label)}
        />
      ) : (
        <Tooltip>
          <TooltipTrigger
            render={
              <span className="truncate flex-1">{display}</span>
            }
          />
          <TooltipContent side="top">
            <div className="space-y-0.5 text-[11px]">
              <div className="font-mono">{ref.local_path}</div>
              {webMode && daemonLabel && (
                <div className="text-muted-foreground">On {daemonLabel}</div>
              )}
              {mismatch && (
                <div className="text-muted-foreground">
                  {isLocalUnknown
                    ? t(($) => $.resources.local_no_daemon_tooltip)
                    : t(($) => $.resources.local_other_machine_tooltip)}
                </div>
              )}
            </div>
          </TooltipContent>
        </Tooltip>
      )}
      {webMode && daemonLabel && !editing && (
        <span
          className="inline-flex max-w-[38%] shrink-0 items-center gap-0.5 rounded bg-muted/60 px-1 py-0.5 text-[9px] text-muted-foreground"
          title={daemonLabel}
        >
          <Server className="size-2.5" aria-hidden />
          <span className="truncate">{daemonLabel}</span>
        </span>
      )}
      {!editing && (
        <button
          type="button"
          disabled={!canEdit || mismatch}
          onClick={() => void onToggleAccess(resource)}
          className={`inline-flex shrink-0 items-center gap-1 rounded-full border px-1.5 py-0.5 text-[10px] font-medium transition-colors ${
            readOnly
              ? "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300"
              : "border-border bg-muted/40 text-muted-foreground"
          } ${canEdit && !mismatch ? "hover:bg-accent" : "cursor-default"}`}
          title={
            !canEdit || mismatch
              ? undefined
              : readOnly
                ? t(($) => $.resources.local_access_make_write)
                : t(($) => $.resources.local_access_make_read)
          }
        >
          {readOnly ? (
            <Lock className="size-2.5" aria-hidden />
          ) : (
            <Pencil className="size-2.5" aria-hidden />
          )}
          {readOnly
            ? t(($) => $.resources.local_access_read)
            : t(($) => $.resources.local_access_write)}
        </button>
      )}
      {canEdit && !mismatch && !editing && (
        <>
          {editingPreview ? (
            <input
              autoFocus
              value={previewDraft}
              placeholder="http://localhost:3000"
              onChange={(e) => setPreviewDraft(e.target.value)}
              onBlur={() => void commitPreview()}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  void commitPreview();
                } else if (e.key === "Escape") {
                  e.preventDefault();
                  setEditingPreview(false);
                  setPreviewDraft(ref.preview_url ?? "");
                }
              }}
              className="w-40 rounded-sm border bg-transparent px-1 py-0.5 text-[11px] outline-none focus-visible:ring-1 focus-visible:ring-ring"
              aria-label={t(($) => $.resources.local_preview_url_label)}
            />
          ) : (
            <button
              type="button"
              onClick={() => {
                setPreviewDraft(ref.preview_url ?? "");
                setEditingPreview(true);
              }}
              className={`shrink-0 rounded-sm p-0.5 transition-opacity hover:bg-accent ${
                ref.preview_url ? "text-brand" : "opacity-0 group-hover:opacity-100 text-muted-foreground"
              }`}
              title={
                ref.preview_url
                  ? t(($) => $.resources.local_preview_url_set, { url: ref.preview_url })
                  : t(($) => $.resources.local_preview_url_add)
              }
            >
              <MonitorPlay className="size-3" aria-hidden />
            </button>
          )}
          <button
            type="button"
            onClick={startEdit}
            className="opacity-0 group-hover:opacity-100 transition-opacity rounded-sm p-0.5 hover:bg-accent"
            title={t(($) => $.resources.local_rename_tooltip)}
          >
            <Pencil className="size-3 text-muted-foreground" />
          </button>
        </>
      )}
      <button
        type="button"
        onClick={onRemove}
        className="opacity-0 group-hover:opacity-100 transition-opacity rounded-sm p-0.5 hover:bg-accent"
        title={t(($) => $.resources.remove_tooltip)}
      >
        <Trash2 className="size-3 text-muted-foreground" />
      </button>
    </div>
  );
}

// Web attach flow for a local_directory. The browser can't OS-pick a folder or
// probe the filesystem, so the human names the host daemon + types an absolute
// path; validation + owner-approval happen on the server and daemon. Desktop
// keeps its native picker (handleAttachLocalDirectory) instead of this.
function AddLocalDirectoryWebPopover({
  wsId,
  daemonOptions,
  attachedDaemonIds,
  submitting,
  onSubmit,
}: {
  wsId: string;
  daemonOptions: DaemonOption[];
  attachedDaemonIds: Set<string>;
  submitting: boolean;
  onSubmit: (v: {
    daemonId: string;
    path: string;
    access: "read" | "write";
    previewUrl: string;
  }) => Promise<boolean>;
}) {
  const [open, setOpen] = useState(false);
  const [browseOpen, setBrowseOpen] = useState(false);
  const [daemonId, setDaemonId] = useState("");
  const [path, setPath] = useState("");
  const [access, setAccess] = useState<"read" | "write">("write");
  const [previewUrl, setPreviewUrl] = useState("");

  // One folder per (project, daemon), so hide daemons already attached here.
  const available = daemonOptions.filter(
    (d) => !attachedDaemonIds.has(d.daemonId),
  );

  // Default to the first attachable daemon when the popover opens (or the
  // current pick drops out of the list), so a lone daemon needs no selection.
  useEffect(() => {
    if (!open) return;
    if (daemonId && available.some((d) => d.daemonId === daemonId)) return;
    setDaemonId(available[0]?.daemonId ?? "");
  }, [open, available, daemonId]);

  const reset = () => {
    setPath("");
    setAccess("write");
    setPreviewUrl("");
  };

  const canSubmit = Boolean(daemonId) && path.trim().length > 0 && !submitting;

  const submit = async () => {
    if (!canSubmit) return;
    const ok = await onSubmit({ daemonId, path, access, previewUrl });
    if (ok) {
      reset();
      setOpen(false);
    }
  };

  const segBase =
    "flex-1 rounded-md border px-2 py-1 text-[11px] font-medium transition-colors";
  const segActive = "border-brand/40 bg-brand/10 text-brand";
  const segIdle =
    "border-border bg-transparent text-muted-foreground hover:bg-accent";

  return (
    <Popover
      open={open}
      onOpenChange={(v) => {
        // The folder picker renders in a portal, so a click inside it reads as
        // "outside" this popover and would dismiss the form underneath it.
        if (!v && browseOpen) return;
        setOpen(v);
        if (!v) reset();
      }}
    >
      <PopoverTrigger
        render={
          <Button
            variant="ghost"
            size="sm"
            className="h-7 w-full justify-start px-2 text-xs text-muted-foreground hover:text-foreground"
          >
            <FolderOpen className="size-3" />
            Add local folder
          </Button>
        }
      />
      <PopoverContent align="start" className="w-72 space-y-2 p-2">
        <div className="text-xs font-medium text-muted-foreground">
          Attach a local folder
        </div>
        {available.length === 0 ? (
          <p className="text-[11px] leading-relaxed text-muted-foreground">
            No online runtime can host a folder. Start a daemon on the machine
            with your code (<span className="font-mono">agora daemon start</span>
            ) or open the desktop app there, then reopen this menu.
          </p>
        ) : (
          <>
            <label className="block space-y-1">
              <span className="text-[10px] uppercase tracking-wide text-muted-foreground">
                Machine
              </span>
              <div className="flex items-center gap-1.5">
                <Server className="size-3.5 shrink-0 text-muted-foreground" />
                <select
                  value={daemonId}
                  onChange={(e) => setDaemonId(e.target.value)}
                  className="h-8 w-full rounded-md border bg-transparent px-2 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring"
                >
                  {available.map((d) => (
                    <option key={d.daemonId} value={d.daemonId}>
                      {d.label}
                      {d.mode === "cloud" ? " (cloud)" : ""}
                    </option>
                  ))}
                </select>
              </div>
            </label>
            <div className="space-y-1">
              <span className="text-[10px] uppercase tracking-wide text-muted-foreground">
                Folder
              </span>
              <div className="flex items-center gap-1.5">
                <input
                  value={path}
                  onChange={(e) => setPath(e.target.value)}
                  placeholder="/Users/you/Projects/app"
                  spellCheck={false}
                  autoCapitalize="none"
                  autoCorrect="off"
                  aria-label="Absolute path"
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      void submit();
                    }
                  }}
                  className="h-8 min-w-0 flex-1 rounded-md border bg-transparent px-2 font-mono text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
                />
                {/* Browse fills the input; the input stays the source of truth
                    so a path can still be typed or corrected afterwards. */}
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="h-8 shrink-0 px-2 text-xs"
                  disabled={!daemonId}
                  onClick={() => setBrowseOpen(true)}
                >
                  <FolderOpen className="size-3" />
                  Browse
                </Button>
              </div>
            </div>
            <div className="space-y-1">
              <span className="text-[10px] uppercase tracking-wide text-muted-foreground">
                Access
              </span>
              <div className="flex items-center gap-1.5">
                <button
                  type="button"
                  onClick={() => setAccess("write")}
                  className={`${segBase} ${access === "write" ? segActive : segIdle}`}
                >
                  Read &amp; write
                </button>
                <button
                  type="button"
                  onClick={() => setAccess("read")}
                  className={`${segBase} ${access === "read" ? segActive : segIdle}`}
                >
                  Read-only
                </button>
              </div>
              {access === "read" && (
                <p className="text-[10px] text-muted-foreground">
                  Runs in an isolated worktree; the folder is never modified.
                </p>
              )}
            </div>
            <label className="block space-y-1">
              <span className="text-[10px] uppercase tracking-wide text-muted-foreground">
                Preview URL (optional)
              </span>
              <input
                value={previewUrl}
                onChange={(e) => setPreviewUrl(e.target.value)}
                placeholder="http://localhost:3000"
                spellCheck={false}
                autoCapitalize="none"
                autoCorrect="off"
                className="h-8 w-full rounded-md border bg-transparent px-2 text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
              />
            </label>
            <p className="text-[10px] leading-relaxed text-muted-foreground">
              The machine&apos;s owner approves the folder before agents run
              there — desktop approves on pick; on a headless daemon run{" "}
              <span className="font-mono">agora daemon allow-dir &lt;path&gt;</span>
              .
            </p>
            <Button
              size="sm"
              className="h-7 w-full text-xs"
              disabled={!canSubmit}
              onClick={() => void submit()}
            >
              {submitting ? "Attaching…" : "Attach folder"}
            </Button>
            <FolderBrowserDialog
              open={browseOpen}
              onOpenChange={setBrowseOpen}
              wsId={wsId}
              daemonId={daemonId}
              daemonLabel={
                available.find((d) => d.daemonId === daemonId)?.label ?? ""
              }
              // Reopen where the typed path points, so correcting a path keeps
              // its place; "" lands on the machine's home.
              initialPath={path.trim()}
              onSelect={setPath}
            />
          </>
        )}
      </PopoverContent>
    </Popover>
  );
}

function CustomRepoForm({
  onSubmit,
}: {
  onSubmit: (url: string) => Promise<void> | void;
}) {
  const { t } = useT("projects");
  const [url, setUrl] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const handle = async (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = url.trim();
    if (!trimmed) return;
    setSubmitting(true);
    try {
      await onSubmit(trimmed);
      setUrl("");
    } finally {
      setSubmitting(false);
    }
  };
  return (
    <form onSubmit={handle} className="flex items-center gap-1.5 pt-1 border-t">
      <input
        type="text"
        value={url}
        onChange={(e) => setUrl(e.target.value)}
        placeholder={t(($) => $.resources.url_placeholder)}
        className="flex-1 bg-transparent text-xs px-2 py-1 outline-none placeholder:text-muted-foreground"
      />
      <Button
        type="submit"
        size="sm"
        variant="ghost"
        className="h-6 px-2 text-xs"
        disabled={!url.trim() || submitting}
      >
        {t(($) => $.resources.url_submit)}
      </Button>
    </form>
  );
}

function localValidationMessage(
  result: ValidateLocalDirectoryResult,
  strings: {
    not_absolute: string;
    not_found: string;
    not_a_directory: string;
    not_readable: string;
    not_writable: string;
    unsupported: string;
    fallback: string;
  },
): string {
  switch (result.reason) {
    case "not_absolute":
      return strings.not_absolute;
    case "not_found":
      return strings.not_found;
    case "not_a_directory":
      return strings.not_a_directory;
    case "not_readable":
      return strings.not_readable;
    case "not_writable":
      return strings.not_writable;
    case "unsupported":
      return strings.unsupported;
    case "error":
    default:
      return result.error ?? strings.fallback;
  }
}
