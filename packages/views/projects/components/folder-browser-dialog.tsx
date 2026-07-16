/* eslint-disable i18next/no-literal-string -- project admin panel; i18n follow-up (matches project-resources-section) */
"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ChevronRight,
  CornerLeftUp,
  Folder,
  GitBranch,
  Link2,
  Loader2,
} from "lucide-react";
import { api } from "@agora/core/api";
import { Button } from "@agora/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@agora/ui/components/ui/dialog";
import { daemonListDir } from "../../platform/daemon-fs-client";

// Folder picker for the web "Add local folder" flow.
//
// A browser cannot OS-pick a folder on the DAEMON's machine — and even for its
// own machine <input type="file"> never exposes an absolute path — so the
// picker walks the daemon's filesystem one directory at a time over
// <daemon_url>/editor/fs/list. Browsing grants nothing: the daemon serves only
// its browsable roots (home + owner-approved folders), and running an agent in
// the folder still needs the machine owner's approval.
interface FolderBrowserDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  wsId: string;
  daemonId: string;
  daemonLabel: string;
  /** Where to open. "" asks the daemon for its default root (the machine's home). */
  initialPath: string;
  onSelect: (path: string) => void;
}

export function FolderBrowserDialog({
  open,
  onOpenChange,
  wsId,
  daemonId,
  daemonLabel,
  initialPath,
  onSelect,
}: FolderBrowserDialogProps) {
  const [cwd, setCwd] = useState(initialPath);

  // Re-anchor on every open so reopening after typing a path lands where the
  // user expects, and switching machines never shows the previous box's tree.
  useEffect(() => {
    if (open) setCwd(initialPath);
  }, [open, initialPath, daemonId]);

  const target = useQuery({
    queryKey: ["daemon-browse-target", wsId, daemonId],
    queryFn: () => api.getDaemonBrowseTarget(daemonId),
    enabled: open && Boolean(daemonId),
  });
  const base = target.data?.daemon_url ?? "";
  const mode = target.data?.mode ?? "";

  const listing = useQuery({
    queryKey: ["daemon-fs", wsId, daemonId, cwd],
    queryFn: () => daemonListDir(base, cwd),
    enabled: open && Boolean(base),
  });

  // The daemon echoes the path it actually resolved (it defaults "" to home),
  // so it — not our request — is what Select commits.
  const resolvedPath = listing.data?.path ?? "";
  const parent = listing.data?.parent ?? "";
  const entries = listing.data?.entries ?? [];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Choose a folder</DialogTitle>
          <DialogDescription>
            Folders on {daemonLabel || "this machine"}. The agent works in the
            folder you pick.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-2">
          <div className="flex items-center gap-1.5">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-7 shrink-0 px-1.5 text-xs"
              disabled={!parent || listing.isFetching}
              onClick={() => setCwd(parent)}
              title={parent ? `Up to ${parent}` : "This is the top of what this machine shares"}
            >
              <CornerLeftUp className="size-3.5" aria-hidden />
            </Button>
            <div
              className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap rounded-md border bg-muted/30 px-2 py-1 font-mono text-[11px]"
              title={resolvedPath}
            >
              {resolvedPath || "…"}
            </div>
          </div>

          <div className="h-64 overflow-y-auto rounded-md border">
            <FolderBrowserBody
              targetLoading={target.isLoading}
              targetError={target.isError}
              mode={mode}
              base={base}
              listingLoading={listing.isLoading || listing.isFetching}
              listingError={listing.error}
              entries={entries}
              onEnter={setCwd}
            />
          </div>

          {listing.data?.truncated === true && (
            <p className="text-[10px] text-muted-foreground">
              Showing the first 1000 folders — type the path directly if the one
              you want isn&apos;t listed.
            </p>
          )}
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-8 text-xs"
            onClick={() => onOpenChange(false)}
          >
            Cancel
          </Button>
          <Button
            type="button"
            size="sm"
            className="h-8 text-xs"
            disabled={!resolvedPath}
            onClick={() => {
              onSelect(resolvedPath);
              onOpenChange(false);
            }}
          >
            Use this folder
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

interface FolderBrowserBodyProps {
  targetLoading: boolean;
  targetError: boolean;
  mode: string;
  base: string;
  listingLoading: boolean;
  listingError: unknown;
  entries: { name: string; path: string; is_git_repo: boolean; is_symlink: boolean }[];
  onEnter: (path: string) => void;
}

// Every branch renders something: an error inside the picker must never close
// it or leave a spinner running forever.
function FolderBrowserBody({
  targetLoading,
  targetError,
  mode,
  base,
  listingLoading,
  listingError,
  entries,
  onEnter,
}: FolderBrowserBodyProps) {
  if (targetLoading) return <BrowserNotice><Loader2 className="size-3.5 animate-spin" aria-hidden /> Connecting to the machine…</BrowserNotice>;
  if (targetError) return <BrowserNotice>Couldn&apos;t reach this machine. Check it&apos;s online, or type the path directly.</BrowserNotice>;
  if (mode === "offline") return <BrowserNotice>This machine is offline. Start its daemon, or type the path directly.</BrowserNotice>;
  // Unknown/blank mode from a newer backend degrades to the same generic state.
  if (!base) return <BrowserNotice>Browsing isn&apos;t available for this machine. Type the path directly.</BrowserNotice>;
  if (listingError) {
    return (
      <BrowserNotice>
        {listingError instanceof Error && listingError.message
          ? listingError.message
          : "Couldn't open that folder."}
      </BrowserNotice>
    );
  }
  if (listingLoading) return <BrowserNotice><Loader2 className="size-3.5 animate-spin" aria-hidden /> Loading…</BrowserNotice>;
  if (entries.length === 0) return <BrowserNotice>No sub-folders here. Use this folder, or go up a level.</BrowserNotice>;

  return (
    <ul className="py-1">
      {entries.map((entry) => (
        <li key={entry.path}>
          <button
            type="button"
            onClick={() => onEnter(entry.path)}
            className="flex w-full items-center gap-2 px-2 py-1.5 text-left text-xs transition-colors hover:bg-accent"
            title={entry.path}
          >
            <Folder className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
            <span className="truncate">{entry.name}</span>
            {entry.is_git_repo && (
              <span
                className="inline-flex shrink-0 items-center gap-0.5 rounded bg-brand/10 px-1 py-0.5 text-[9px] font-medium text-brand"
                title="Git repository"
              >
                <GitBranch className="size-2.5" aria-hidden />
                repo
              </span>
            )}
            {entry.is_symlink && (
              <Link2 className="size-2.5 shrink-0 text-muted-foreground" aria-hidden />
            )}
            <ChevronRight className="ml-auto size-3 shrink-0 text-muted-foreground" aria-hidden />
          </button>
        </li>
      ))}
    </ul>
  );
}

function BrowserNotice({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-full items-center justify-center gap-1.5 px-4 text-center text-[11px] text-muted-foreground">
      {children}
    </div>
  );
}
