"use client";

import { useQuery } from "@tanstack/react-query";
import { ChevronDown, FolderOpen } from "lucide-react";
import { projectResourcesOptions } from "@agora/core/projects";
import type { LocalDirectoryResourceRef, ProjectResource } from "@agora/core/types";
import { useWorkspaceId } from "@agora/core/hooks";
import { useLocalDaemonStatus } from "../../platform";
import { useT } from "../../i18n";

/**
 * Banner shown at the top of the issue's Activity section when the
 * project is pinned to a `local_directory` resource on **this** daemon.
 * Tells the user "starting an agent here will use {label} ({path}) in-
 * place" so they notice they are not getting an isolated git worktree.
 *
 * Rendered only on desktop: web has no daemon to compare against, so the
 * "this machine" check would always fail. Web users will see local_directory
 * resources read-only in the sidebar but no Activity-section hint.
 *
 * SSR-safe: the underlying hook reads `window.daemonAPI` defensively, so
 * server renders return null.
 */
export function LocalDirectoryHint({
  projectId,
}: {
  projectId: string | null | undefined;
}) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const daemon = useLocalDaemonStatus();
  const { data: resources = [] } = useQuery({
    ...projectResourcesOptions(wsId, projectId ?? ""),
    enabled: Boolean(projectId),
  });

  if (!projectId) return null;
  if (!daemon.daemonId) return null;

  const matches: Array<ProjectResource & { resource_ref: LocalDirectoryResourceRef }> =
    resources
      .filter(
        (r): r is ProjectResource & { resource_ref: LocalDirectoryResourceRef } =>
          r.resource_type === "local_directory",
      )
      .filter((r) => r.resource_ref.daemon_id === daemon.daemonId);

  if (matches.length === 0) return null;

  return (
    <details className="group mt-2 rounded-md border border-dashed bg-muted/25 text-xs text-muted-foreground">
      <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2 transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <FolderOpen className="size-3.5 shrink-0" />
        <span>{t(($) => $.resources.local_workspace_count, { count: matches.length })}</span>
        <ChevronDown className="ml-auto size-3.5 transition-transform group-open:rotate-180 motion-reduce:transition-none" />
      </summary>
      <div className="space-y-1 border-t border-dashed px-3 py-2">
        {matches.map((resource) => {
          const ref = resource.resource_ref;
          const label = (ref.label || resource.label || ref.local_path).trim() || ref.local_path;
          return (
            <div key={resource.id} className="flex min-w-0 items-center gap-2 pl-5">
              <span className="shrink-0 font-medium text-foreground">{label}</span>
              <span className="truncate font-mono opacity-60">{ref.local_path}</span>
            </div>
          );
        })}
      </div>
    </details>
  );
}
