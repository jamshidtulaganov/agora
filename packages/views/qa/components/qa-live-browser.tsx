"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Globe, ChevronRight, Loader2 } from "lucide-react";
import { api } from "@agora/core/api";
import { cn } from "@agora/ui/lib/utils";
import { EditorBrowserPane } from "../../issues/components/editor-browser-pane";
import { useT } from "../../i18n";

// Live, interactive browser — ON the QA review page, not behind an "Open
// full issue" link. The same CDP screencast the dev-oriented Code panel
// already proved (EditorBrowserPane): a human can watch and drive a real
// Chromium against the issue's worktree to reproduce a bug or sanity-check a
// fix, without leaving the QA instrument surface. No new daemon endpoint —
// this reuses GET /api/issues/:id/editor + /editor/browser/* exactly as the
// Code panel does, scoped to the same most-recently-active agent's worktree.
//
// Self-host only (mode === "self-host"); cloud mode and "no worktree yet"
// both degrade to nothing rendered — never a broken pane. Lazy: the daemon
// only spawns/streams Chromium once a human expands this section (same
// "nothing runs until opened" contract as the Code panel).

export function QALiveBrowser({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const [open, setOpen] = useState(false);
  const { data, isLoading } = useQuery({
    queryKey: ["issue-editor", issueId],
    queryFn: () => api.getIssueEditor(issueId),
    enabled: open,
    staleTime: 30_000,
  });

  const agent = data?.mode === "self-host" ? data.agents[0] : undefined;
  const available = data?.mode === "self-host" && !!data.daemon_url && !!agent;
  // Cloud mode and "no worktree yet" (mode === "") both have nothing to show
  // here — degrade silently rather than render a broken pane or an error for
  // a state that's entirely expected (e.g. before the first dev task runs).
  const unavailable = open && !isLoading && data && !available;

  return (
    <section className="rounded-lg border">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-accent/40"
      >
        <ChevronRight
          aria-hidden
          className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", open && "rotate-90")}
        />
        <Globe className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="text-[12px] font-medium">{t(($) => $.qa_review.live_browser)}</span>
        {!open && (
          <span className="text-[11px] text-muted-foreground">{t(($) => $.qa_review.live_browser_hint)}</span>
        )}
      </button>
      {open && (
        <div className="border-t">
          {isLoading ? (
            <div className="flex items-center gap-1.5 px-3 py-4 text-[12px] text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" />
              {t(($) => $.qa_review.live_browser_loading)}
            </div>
          ) : available && data ? (
            <div className="h-[480px]">
              <EditorBrowserPane daemonUrl={data.daemon_url} workdir={agent!.work_dir} />
            </div>
          ) : unavailable ? (
            <p className="px-3 py-4 text-[12px] text-muted-foreground">
              {t(($) => $.qa_review.live_browser_unavailable)}
            </p>
          ) : null}
        </div>
      )}
    </section>
  );
}
