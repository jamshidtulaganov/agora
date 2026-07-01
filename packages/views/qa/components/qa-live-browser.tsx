"use client";

import { useState } from "react";
import { Globe, Loader2, Play, ExternalLink } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@agora/core/api";
import { Button } from "@agora/ui/components/ui/button";
import { EditorBrowserPane } from "../../issues/components/editor-browser-pane";
import { useT } from "../../i18n";

// The "Live testing" bay — a PERSISTENT right-rail panel (not a collapsible
// inline section), so a QA engineer can watch and drive the running app while
// reading the verdict and checks beside it. Two independent live sources,
// tried in this order:
//
// 1. Self-host CDP (EditorBrowserPane) — a real Chromium driven against an
//    AGENT's own per-issue worktree. Requires a live daemon + spawns Chromium
//    on click ("Start"), so it stays opt-in.
// 2. Deployed QA preview (qa-preview-url) — the project's standing QA target
//    (a connected box, e.g. agora.sdteam.uz, or a configured qa_smoke_url).
//    For a workspace whose QA target is a deployed environment rather than a
//    per-issue worktree (a monolith QA'd by deploying a branch to a box, not
//    by an agent driving its own Chromium), this is the ONLY live source that
//    ever resolves — and since it's already running, it loads immediately, no
//    Start click, no spawn cost.
//
// Editor metadata is fetched eagerly (cheap — no Chromium) so the bay always
// shows the right state. Self-host-CDP is self-host only; cloud degrades
// straight to the preview-URL fallback, then to the empty state.

export function QALiveBrowser({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const [started, setStarted] = useState(false);

  const { data, isLoading } = useQuery({
    queryKey: ["issue-editor", issueId],
    queryFn: () => api.getIssueEditor(issueId),
    staleTime: 30_000,
  });
  const { data: preview, isLoading: previewLoading } = useQuery({
    queryKey: ["issue-qa-preview-url", issueId],
    queryFn: () => api.getIssueQAPreviewURL(issueId),
    staleTime: 30_000,
  });

  const agent = data?.mode === "self-host" ? data.agents[0] : undefined;
  const available = data?.mode === "self-host" && !!data.daemon_url && !!agent;
  const streaming = started && available && !!data;
  const previewUrl = preview?.url ?? "";
  // Server-checked: an iframe embed is only attempted when we've confirmed the
  // target's response headers actually allow cross-origin framing. A CSP
  // frame-ancestors or X-Frame-Options block renders an iframe silently
  // blank — no JS error to detect after the fact — so this MUST be decided
  // before attempting the embed, not discovered from a failed render.
  const previewEmbeddable = !!preview?.embeddable && !!previewUrl;
  const previewLinkOnly = !isLoading && !available && previewUrl && !previewEmbeddable;

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
      ) : previewEmbeddable ? (
        <div className="flex min-h-0 flex-1 flex-col">
          <div className="flex items-center gap-2 border-b bg-muted/20 px-3 py-1.5">
            <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground">
              {previewUrl}
            </span>
            <a
              href={previewUrl}
              target="_blank"
              rel="noreferrer"
              className="flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground"
            >
              {t(($) => $.qa_review.live_preview_open)}
              <ExternalLink className="size-3" />
            </a>
          </div>
          <iframe
            src={previewUrl}
            title={t(($) => $.qa_review.live_testing)}
            className="min-h-0 flex-1 border-0 bg-white"
          />
        </div>
      ) : previewLinkOnly ? (
        // The target's own headers block cross-origin framing (checked
        // server-side) — an iframe here would just render blank. Show a
        // real, working affordance instead of a broken embed.
        <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-10 text-center">
          <span className="flex size-11 items-center justify-center rounded-full bg-muted">
            <Globe className="size-5 text-muted-foreground" aria-hidden />
          </span>
          <p className="max-w-[260px] text-[11px] leading-relaxed text-muted-foreground">
            {t(($) => $.qa_review.live_preview_not_embeddable)}
          </p>
          <a
            href={previewUrl}
            target="_blank"
            rel="noreferrer"
            className="flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-[12px] font-medium hover:bg-accent"
          >
            {previewUrl}
            <ExternalLink className="size-3.5" />
          </a>
        </div>
      ) : (
        <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-10 text-center">
          {isLoading || previewLoading ? (
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
