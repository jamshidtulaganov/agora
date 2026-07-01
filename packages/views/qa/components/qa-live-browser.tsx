"use client";

import { useState } from "react";
import { Globe, Loader2, Play } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@agora/core/api";
import { Button } from "@agora/ui/components/ui/button";
import { EditorBrowserPane } from "../../issues/components/editor-browser-pane";
import { useT } from "../../i18n";

// The "Live testing" bay — a PERSISTENT right-rail panel (not a collapsible
// inline section), so a QA engineer can watch and drive the running app while
// reading the verdict and checks beside it. The same CDP screencast the dev
// Code panel proved (EditorBrowserPane): a real Chromium against the issue's
// worktree, driveable inside the QA surface.
//
// Editor metadata is fetched eagerly (cheap — no Chromium) so the bay always
// shows the right state: Start affordance when a live worktree exists, a quiet
// empty state when it doesn't. Chromium only spawns when the human clicks
// Start and EditorBrowserPane mounts — the "nothing runs until asked" contract
// the Code panel already honors. Self-host only; cloud / no-worktree degrade to
// the empty state rather than a blank pane.

export function QALiveBrowser({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const [started, setStarted] = useState(false);

  const { data, isLoading } = useQuery({
    queryKey: ["issue-editor", issueId],
    queryFn: () => api.getIssueEditor(issueId),
    staleTime: 30_000,
  });

  const agent = data?.mode === "self-host" ? data.agents[0] : undefined;
  const available = data?.mode === "self-host" && !!data.daemon_url && !!agent;
  const streaming = started && available && !!data;

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border bg-card">
      <div className="flex items-center gap-2 border-b px-3 py-2">
        <Globe className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="text-[12px] font-medium">{t(($) => $.qa_review.live_testing)}</span>
        {streaming && (
          <span className="ml-auto flex items-center gap-1.5 text-[10px] uppercase tracking-wide text-muted-foreground">
            <span aria-hidden className="size-1.5 rounded-full bg-emerald-500 motion-safe:animate-pulse" />
            {t(($) => $.qa_review.live_on)}
          </span>
        )}
      </div>

      {streaming ? (
        <div className="min-h-0 flex-1">
          <EditorBrowserPane daemonUrl={data.daemon_url} workdir={agent!.work_dir} />
        </div>
      ) : (
        <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-10 text-center">
          {isLoading ? (
            <div className="flex items-center gap-1.5 text-[12px] text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" />
              {t(($) => $.qa_review.live_browser_loading)}
            </div>
          ) : available ? (
            <>
              <span className="flex size-11 items-center justify-center rounded-full bg-muted">
                <Globe className="size-5 text-muted-foreground" aria-hidden />
              </span>
              <p className="max-w-[240px] text-[11px] leading-relaxed text-muted-foreground">
                {t(($) => $.qa_review.live_browser_hint)}
              </p>
              <Button
                type="button"
                size="sm"
                className="h-8 gap-1.5 text-[12px]"
                onClick={() => setStarted(true)}
              >
                <Play className="size-3.5" />
                {t(($) => $.qa_review.live_browser_start)}
              </Button>
            </>
          ) : (
            <>
              <span className="flex size-11 items-center justify-center rounded-full bg-muted">
                <Globe className="size-5 text-muted-foreground/50" aria-hidden />
              </span>
              <p className="max-w-[240px] text-[11px] leading-relaxed text-muted-foreground">
                {t(($) => $.qa_review.live_browser_unavailable)}
              </p>
            </>
          )}
        </div>
      )}
    </div>
  );
}
