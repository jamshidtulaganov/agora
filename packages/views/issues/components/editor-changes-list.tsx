/* eslint-disable i18next/no-literal-string -- co-code editor surface; i18n follow-up */
"use client";

import { useQuery } from "@tanstack/react-query";
import { FileDiff, GitBranch } from "lucide-react";
import { proxyHeaders, absoluteBase } from "./editor-proxy-fetch";

// Inline "what changed" — the committed/working file list for the agent's
// worktree, so a reviewer sees which files moved (+adds / −dels) without opening
// code-server's Source Control. Reads the daemon /editor/changes endpoint
// (same query key as the review bar → one shared, polled fetch). Renders nothing
// when there are no changes, so it never clutters an idle Activity tab.

interface RepoChange {
  repo: string;
  branch: string;
  base: string;
  files: { path: string; additions: number; deletions: number }[];
}

export function EditorChangesList({
  daemonUrl,
  workdir,
}: {
  daemonUrl: string;
  workdir: string;
}) {
  const { data } = useQuery({
    queryKey: ["editor-changes", workdir],
    queryFn: async () => {
      const r = await fetch(`${absoluteBase(daemonUrl)}/editor/changes`, {
        method: "POST",
        headers: proxyHeaders(daemonUrl),
        body: JSON.stringify({ workdir }),
      });
      if (!r.ok) throw new Error("changes lookup failed");
      return (await r.json()) as { repos: RepoChange[] };
    },
    enabled: !!daemonUrl && !!workdir,
    refetchInterval: 8000,
  });

  const repos = (data?.repos ?? []).filter((r) => r.files.length > 0);
  if (repos.length === 0) return null;

  const total = repos.reduce((n, r) => n + r.files.length, 0);

  return (
    <div className="space-y-2 rounded-md border border-border bg-muted/20 p-2">
      <div className="px-1 text-[11px] font-medium text-foreground">
        What changed · {total} file{total === 1 ? "" : "s"}
      </div>
      {repos.map((r) => (
        <div key={r.repo}>
          {/* The repo name is only meaningful when more than one repo changed;
              for the common single-repo case it's noise, so it's dropped. */}
          {repos.length > 1 && (
            <div className="flex items-center gap-1 px-1 text-[10px] uppercase tracking-wide text-muted-foreground">
              <GitBranch className="h-3 w-3 shrink-0" />
              <span className="truncate">{r.repo}</span>
            </div>
          )}
          <div className="mt-0.5">
            {r.files.map((f) => {
              // Lead with the filename (what the reader recognises); trail the
              // folder path dimmed, instead of a raw mono path.
              const slash = f.path.lastIndexOf("/");
              const dir = slash >= 0 ? f.path.slice(0, slash + 1) : "";
              const name = slash >= 0 ? f.path.slice(slash + 1) : f.path;
              return (
                <div
                  key={f.path}
                  className="flex items-center gap-2 rounded px-1.5 py-0.5 text-[11px]"
                  title={f.path}
                >
                  <FileDiff className="h-3 w-3 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 flex-1 truncate">
                    <span className="text-foreground">{name}</span>
                    {dir && (
                      <span className="text-muted-foreground/70"> · {dir}</span>
                    )}
                  </span>
                  {f.additions > 0 && (
                    <span className="shrink-0 text-emerald-600 dark:text-emerald-400">
                      +{f.additions}
                    </span>
                  )}
                  {f.deletions > 0 && (
                    <span className="shrink-0 text-destructive">
                      −{f.deletions}
                    </span>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      ))}
    </div>
  );
}
