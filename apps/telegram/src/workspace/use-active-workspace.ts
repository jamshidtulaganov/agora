import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { workspaceListOptions } from "@agora/core/workspace/queries";
import { setCurrentWorkspace } from "@agora/core/platform";
import type { Workspace } from "@agora/core/types";

const LAST_WS_KEY = "tg_ws_slug";

function readLastSlug(): string | null {
  try {
    return localStorage.getItem(LAST_WS_KEY);
  } catch {
    return null;
  }
}

function writeLastSlug(slug: string) {
  try {
    localStorage.setItem(LAST_WS_KEY, slug);
  } catch {
    /* ignore */
  }
}

export interface ActiveWorkspace {
  workspaces: Workspace[];
  active: Workspace | null;
  isLoading: boolean;
  select: (slug: string) => void;
}

// Resolves and tracks the active workspace for the Mini App. SD Telegram users
// belong to the three sd-* workspaces; we default to the last-used (or first)
// and let them switch. Sets BOTH the api header source (setCurrentWorkspace, for
// X-Workspace-Slug) and exposes the active workspace so the shell can mount
// WorkspaceSlugProvider (for useWorkspaceId reactivity).
export function useActiveWorkspace(preferredSlug?: string | null): ActiveWorkspace {
  const { data: workspaces = [], isLoading } = useQuery(workspaceListOptions());
  // A deep-link workspace (from a bot button) wins on first launch over the
  // last-used one, so the linked issue opens in its own workspace.
  const [slug, setSlug] = useState<string | null>(() => preferredSlug ?? readLastSlug());

  const active =
    (slug ? workspaces.find((w) => w.slug === slug) : undefined) ??
    workspaces[0] ??
    null;

  useEffect(() => {
    if (!active) return;
    setCurrentWorkspace(active.slug, active.id);
    writeLastSlug(active.slug);
    if (active.slug !== slug) setSlug(active.slug);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active?.id]);

  // A deep-link workspace can resolve AFTER first render (e.g. a locate() call
  // that derives the issue's workspace from its UUID). When it lands and the
  // user belongs to it, switch — so a link opened in the wrong workspace heals.
  useEffect(() => {
    if (!preferredSlug || preferredSlug === slug) return;
    if (workspaces.some((w) => w.slug === preferredSlug)) setSlug(preferredSlug);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [preferredSlug, workspaces.length]);

  const select = (next: string) => {
    const ws = workspaces.find((w) => w.slug === next);
    if (!ws) return;
    setCurrentWorkspace(ws.slug, ws.id);
    writeLastSlug(ws.slug);
    setSlug(ws.slug);
  };

  return { workspaces, active, isLoading, select };
}
