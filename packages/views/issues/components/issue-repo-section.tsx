/* eslint-disable i18next/no-literal-string -- issue repo admin panel; i18n follow-up */
"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  AlertCircle,
  ChevronRight,
  FolderGit,
  FolderOpen,
  Plus,
  Search,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import {
  projectResourcesOptions,
  useCreateProjectResource,
  useDeleteProjectResource,
} from "@agora/core/projects";
import { useWorkspaceId } from "@agora/core/hooks";
import { useCurrentWorkspace, useWorkspacePaths } from "@agora/core/paths";
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
import { AppLink } from "../../navigation";

// Right-panel "Repository" section on an issue. Surfaces which repo(s) the
// issue's PROJECT is bound to (project_resource: github_repo / local_directory)
// so a human + the agents know where the code lives the moment a task starts.
// When none is connected it PROMPTS to connect one — otherwise an assigned
// agent runs in a slim worktree with no project code and nobody knows why.
// Multiple repos per project are supported (the vision's multi-repo case): the
// connect affordance stays available so more can be added.
//
// Plain strings (no i18n), matching the other dev-workspace sections.

function isGithubRef(
  r: ProjectResource,
): r is ProjectResource & { resource_ref: GithubRepoResourceRef } {
  return r.resource_type === "github_repo";
}
function isLocalDirRef(
  r: ProjectResource,
): r is ProjectResource & { resource_ref: LocalDirectoryResourceRef } {
  return r.resource_type === "local_directory";
}

// owner/repo from a git URL, for a compact label.
function shortRepo(url: string): string {
  const m = url.replace(/\.git$/i, "").match(/[:/]([^/]+\/[^/]+?)$/);
  return m ? m[1]! : url;
}

export function IssueRepoSection({
  projectId,
}: {
  projectId: string | null | undefined;
}) {
  const wsId = useWorkspaceId();
  const workspace = useCurrentWorkspace();
  const paths = useWorkspacePaths();
  const [open, setOpen] = useState(true);
  const [addOpen, setAddOpen] = useState(false);
  const [repoSearch, setRepoSearch] = useState("");
  const [customUrl, setCustomUrl] = useState("");

  const { data: resources = [] } = useQuery({
    ...projectResourcesOptions(wsId, projectId ?? ""),
    enabled: !!projectId,
  });
  const createResource = useCreateProjectResource(wsId, projectId ?? "");
  const deleteResource = useDeleteProjectResource(wsId, projectId ?? "");

  // Issue not in a project — no repo concept to surface.
  if (!projectId) return null;

  const githubRepos = resources.filter(isGithubRef);
  const localDirs = resources.filter(isLocalDirRef);
  const hasCode = githubRepos.length > 0 || localDirs.length > 0;

  const attachedUrls = new Set(githubRepos.map((r) => r.resource_ref.url));
  const repoQuery = repoSearch.trim().toLowerCase();
  const filteredRepos =
    workspace?.repos?.filter(
      (repo) =>
        repo.url.toLowerCase().includes(repoQuery) &&
        !attachedUrls.has(repo.url),
    ) ?? [];

  const attach = async (url: string) => {
    const trimmed = url.trim();
    if (!trimmed) return;
    try {
      await createResource.mutateAsync({
        resource_type: "github_repo",
        resource_ref: { url: trimmed },
      });
      toast.success("Repository connected");
      setAddOpen(false);
      setRepoSearch("");
      setCustomUrl("");
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : "Failed to connect repository",
      );
    }
  };

  // Detach a repo/local dir from the PROJECT. Project-scoped (same resource the
  // project-settings sidebar manages) — surfaced here so a human can correct a
  // mis-connected repo without leaving the issue. Hover-reveal so it can't be
  // fat-fingered mid-task; re-attach is one click away, so no confirm modal.
  const remove = async (resource: ProjectResource) => {
    try {
      await deleteResource.mutateAsync(resource.id);
      toast.success("Repository disconnected");
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : "Failed to disconnect repository",
      );
    }
  };

  // One shared Popover; rendered in exactly one branch per render (empty-state
  // prompt OR the "connect another" footer), so the shared open state is safe.
  const connectButton = (
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
            variant={hasCode ? "ghost" : "default"}
            size="sm"
            className={
              hasCode
                ? "h-7 px-2 text-xs text-muted-foreground hover:text-foreground"
                : "h-7 px-2 text-xs"
            }
          >
            <Plus className="size-3" />
            {hasCode ? "Connect another" : "Connect repository"}
          </Button>
        }
      />
      <PopoverContent align="start" className="w-72 space-y-2 p-2">
        <div className="text-xs font-medium text-muted-foreground">
          Connect a GitHub / GitLab repository
        </div>
        {workspace?.repos && workspace.repos.length > 0 && (
          <>
            <div className="relative">
              <Search className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <input
                type="text"
                value={repoSearch}
                onChange={(e) => setRepoSearch(e.target.value)}
                placeholder="Search repositories…"
                aria-label="Search repositories"
                className="h-8 w-full rounded-md border bg-transparent pl-7 pr-2 text-xs outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
              />
            </div>
            <div className="max-h-48 space-y-1 overflow-y-auto">
              {filteredRepos.length === 0 && repoQuery && (
                <p className="py-2 text-center text-xs text-muted-foreground">
                  No matching repositories
                </p>
              )}
              {filteredRepos.map((repo) => (
                <button
                  key={repo.url}
                  type="button"
                  disabled={createResource.isPending}
                  onClick={() => void attach(repo.url)}
                  className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs transition-colors hover:bg-accent disabled:cursor-not-allowed disabled:opacity-50"
                >
                  <FolderGit className="size-3.5 shrink-0" />
                  <span className="flex-1 truncate">{repo.url}</span>
                </button>
              ))}
            </div>
          </>
        )}
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void attach(customUrl);
          }}
          className="flex items-center gap-1.5 border-t pt-1"
        >
          <input
            type="text"
            value={customUrl}
            onChange={(e) => setCustomUrl(e.target.value)}
            placeholder="https://github.com/owner/repo"
            className="flex-1 bg-transparent px-2 py-1 text-xs outline-none placeholder:text-muted-foreground"
          />
          <Button
            type="submit"
            size="sm"
            variant="ghost"
            className="h-6 px-2 text-xs"
            disabled={!customUrl.trim() || createResource.isPending}
          >
            Add
          </Button>
        </form>
        <p className="border-t pt-1.5 text-[10px] leading-snug text-muted-foreground">
          To open branches &amp; PRs, the connected GitHub/GitLab token needs
          Contents (read &amp; write) + Pull requests (write).
        </p>
      </PopoverContent>
    </Popover>
  );

  return (
    <div>
      <button
        type="button"
        className={`mb-2 flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors hover:bg-accent/70 ${open ? "" : "text-muted-foreground hover:text-foreground"}`}
        onClick={() => setOpen(!open)}
      >
        Repository
        {!hasCode && (
          <span
            className="ml-0.5 inline-flex size-1.5 rounded-full bg-amber-500"
            aria-label="no repository connected"
          />
        )}
        <ChevronRight
          className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`}
        />
      </button>

      {open && (
        <div className="space-y-2 pl-2">
          {!hasCode ? (
            <div className="space-y-2 rounded-md border border-amber-500/30 bg-amber-500/5 p-2.5">
              <div className="flex gap-2">
                <AlertCircle className="mt-0.5 size-3.5 shrink-0 text-amber-600 dark:text-amber-500" />
                <p className="text-xs text-muted-foreground">
                  No repository connected to this project. Agents have no code to
                  work on — connect one so they can clone, edit and open PRs.
                </p>
              </div>
              <div className="flex items-center gap-2">
                {connectButton}
                <AppLink
                  href={paths.projectDetail(projectId)}
                  className="text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
                >
                  Project settings
                </AppLink>
              </div>
            </div>
          ) : (
            <>
              <div className="space-y-1.5">
                {githubRepos.map((r) => (
                  <div
                    key={r.id}
                    className="group flex items-center gap-2 text-xs"
                  >
                    <FolderGit className="size-3.5 shrink-0 text-muted-foreground" />
                    <Tooltip>
                      <TooltipTrigger
                        render={
                          <a
                            href={r.resource_ref.url}
                            target="_blank"
                            rel="noopener noreferrer"
                            className="flex-1 truncate hover:underline"
                          >
                            {r.label || shortRepo(r.resource_ref.url)}
                          </a>
                        }
                      />
                      <TooltipContent side="top">
                        {r.resource_ref.url}
                      </TooltipContent>
                    </Tooltip>
                    {r.resource_ref.default_branch_hint && (
                      <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                        {r.resource_ref.default_branch_hint}
                      </span>
                    )}
                    <button
                      type="button"
                      onClick={() => void remove(r)}
                      disabled={deleteResource.isPending}
                      className="shrink-0 rounded-sm p-0.5 opacity-0 transition-opacity hover:bg-accent group-hover:opacity-100 disabled:opacity-50"
                      title="Disconnect repository"
                      aria-label="Disconnect repository"
                    >
                      <Trash2 className="size-3 text-muted-foreground" />
                    </button>
                  </div>
                ))}
                {localDirs.map((r) => (
                  <div key={r.id} className="group flex items-center gap-2 text-xs">
                    <FolderOpen className="size-3.5 shrink-0 text-muted-foreground" />
                    <span className="flex-1 truncate">
                      {r.resource_ref.label ||
                        r.label ||
                        r.resource_ref.local_path}
                    </span>
                    <button
                      type="button"
                      onClick={() => void remove(r)}
                      disabled={deleteResource.isPending}
                      className="shrink-0 rounded-sm p-0.5 opacity-0 transition-opacity hover:bg-accent group-hover:opacity-100 disabled:opacity-50"
                      title="Disconnect"
                      aria-label="Disconnect local directory"
                    >
                      <Trash2 className="size-3 text-muted-foreground" />
                    </button>
                  </div>
                ))}
              </div>
              {connectButton}
            </>
          )}
        </div>
      )}
    </div>
  );
}
