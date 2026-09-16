"use client";

import { useQuery } from "@tanstack/react-query";
import { X } from "lucide-react";
import { toast } from "sonner";
import { assistantSessionListOptions, useUpdateAssistantSession } from "@agora/core/assistant";
import { workspaceListOptions } from "@agora/core/workspace";
import { useT } from "../../i18n";

interface AssistantContextChipProps {
  sessionId: string;
}

/**
 * The session's focus workspace, shown next to the composer.
 *
 * This exists because the assistant is user-scoped while its tools are not:
 * a session carries a focus workspace, and that focus — not whichever page
 * the user happens to have open — is what a tool defaults to. Opening the
 * panel from another workspace and typing "close the login bug" would
 * otherwise silently act somewhere else. Plan §3, "Resolve context explicitly".
 *
 * Removing the chip clears the focus, which widens the session to every
 * workspace the user belongs to rather than silently re-pointing it.
 *
 * Both queries read the cache the surrounding surface already populated
 * (`enabled: false`), so the chip never adds a request of its own.
 */
export function AssistantContextChip({ sessionId }: AssistantContextChipProps) {
  const { t } = useT("assistant");
  const { data: sessions = [] } = useQuery({ ...assistantSessionListOptions(), enabled: false });
  const { data: workspaces = [] } = useQuery({ ...workspaceListOptions(), enabled: false });
  const updateSession = useUpdateAssistantSession();

  const focusWorkspaceId = sessions.find((s) => s.id === sessionId)?.focus_workspace_id ?? null;
  if (!focusWorkspaceId) return null;

  // The slug is what the user recognises from the URL, and it is what the
  // confirmation card shows for a target — keep the two labels identical.
  // An unresolved id (a workspace they just lost access to, or a list that
  // hasn't loaded on this surface) still renders a chip: "scoped somewhere"
  // is more honest than showing nothing.
  const slug = workspaces.find((w) => w.id === focusWorkspaceId)?.slug ?? null;

  const handleRemove = () => {
    updateSession.mutate(
      { sessionId, focus_workspace_id: null },
      { onError: () => toast.error(t(($) => $.context.clear_failed)) },
    );
  };

  return (
    <span
      title={t(($) => $.context.tooltip)}
      className="inline-flex max-w-full items-center gap-1 rounded-full border border-border bg-muted/50 py-0.5 pl-2 pr-0.5 text-xs text-muted-foreground"
    >
      <span className="truncate">
        {slug ?? t(($) => $.context.unknown_workspace)}
      </span>
      <button
        type="button"
        onClick={handleRemove}
        disabled={updateSession.isPending}
        aria-label={t(($) => $.context.clear)}
        className="inline-flex size-4 shrink-0 cursor-pointer items-center justify-center rounded-full transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-default disabled:opacity-50"
      >
        <X className="size-3" />
      </button>
    </span>
  );
}
