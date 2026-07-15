"use client";

import { Globe, Loader2, ExternalLink, X } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@agora/core/api";
import { Button } from "@agora/ui/components/ui/button";
import { EditorBrowserPane } from "../../issues/components/editor-browser-pane";
import { useT } from "../../i18n";

// The "Live testing" bay. Signal-driven, not default-on: most QA is API or
// unit-level and never touches a browser, so this bay is a compact
// icon+hint+button card until something worth watching exists (see `active`
// below) — mounting the full pane used to auto-connect a CDP Chromium AND
// auto-boot a dev server on the daemon as a side effect of merely opening the
// QA lens, which is expensive and almost always unwanted.
//
// `active` is the mount-gate — owned by the parent (QALensBody), which
// derives it from either a live QA-squad run (useQaRunningTasks) or the user
// explicitly clicking "Open live testing". When `active` is false: the
// browser/preview queries below are `enabled: false` (zero network), and this
// renders ONLY the compact card — no EditorBrowserPane, no WebSocket, no
// Chromium, no autoPreview dev-server boot.
//
// When `active` is true, this is a PERSISTENT panel (not a collapsible inline
// section) so a QA engineer can watch and drive the running app while reading
// the verdict and checks beside it — with a small collapse control in the
// header so the reviewer can put it away again (sticky for this mount; see
// QALensBody's open-state effect).
//
// QA ALWAYS drives a real Chromium (CDP), never an <iframe>. The deployed QA
// target (agora.sdteam.uz and the like) sends a CSP `frame-ancestors` that
// blanks any cross-origin embed — so an iframe would silently show nothing.
// A CDP browser navigates top-level, which that header does not restrict, so
// the same URL loads and stays interactive. Live sources, in order:
//
// 1. Deployed QA target in CDP (preferred) — the project's standing QA box
//    (connected box, e.g. agora.sdteam.uz, or a configured qa_smoke_url),
//    opened in a Chromium keyed by the target URL. Keying by URL means every
//    issue that shares the box reuses ONE warm Chromium — the daemon keeps it
//    alive between opens, so only the very first open pays the spawn.
// 2. New-tab link — no reachable daemon; hand off to the browser.
//
// A mutable development worktree is deliberately not a QA source. Product
// verification must target the configured QA environment or the immutable
// integration artifact surfaced by the Product/Evidence views.
//
// Editor + preview metadata are only fetched while active (cheap — no
// Chromium by themselves, but still real requests we don't want to fire on
// every lens open). CDP works in BOTH modes: self-host dials the local daemon
// directly; cloud rides the backend's /browser/proxy/<token> reverse-proxy to
// the remote daemon — the SAME daemon the QA agent attaches to, so the
// reviewer watches the run.

export function QALiveBrowser({
  issueId,
  active,
  running,
  onOpen,
  onCollapse,
}: {
  issueId: string;
  // Mount-gate: false = compact idle/manual-closed card, zero network.
  active: boolean;
  // A QA-squad task is running RIGHT NOW on this issue (may be true even
  // while active=false, if the reviewer manually collapsed a live bay — the
  // compact card surfaces that with the same "Live" pulse used when open).
  running: boolean;
  onOpen: () => void;
  onCollapse: () => void;
}) {
  const { t } = useT("issues");

  const { data: browser, isLoading: browserLoading } = useQuery({
    queryKey: ["issue-browser", issueId],
    queryFn: () => api.getIssueBrowser(issueId),
    staleTime: 30_000,
    enabled: active,
  });
  const { data: preview, isLoading: previewLoading } = useQuery({
    queryKey: ["issue-qa-preview-url", issueId],
    queryFn: () => api.getIssueQAPreviewURL(issueId),
    staleTime: 30_000,
    enabled: active,
  });

  // Idle / manually-collapsed: a compact card, nothing else. No
  // EditorBrowserPane, no CDP, no autoPreview boot — just the affordance to
  // open it. `running` still surfaces the live pulse so a reviewer who
  // collapsed a live run mid-flight can see it's still going.
  if (!active) {
    return (
      <div className="flex items-center gap-3 rounded-xl border border-dashed bg-muted/20 px-4 py-4">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-muted">
          <Globe className="size-4 text-muted-foreground" aria-hidden />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <p className="text-[12px] font-medium">{t(($) => $.qa_review.live_testing)}</p>
            {running && (
              <span className="flex shrink-0 items-center gap-1.5 text-[10px] uppercase tracking-wide text-muted-foreground">
                <span aria-hidden className="size-1.5 rounded-full bg-emerald-500 motion-safe:animate-pulse" />
                {t(($) => $.qa_review.live_on)}
              </span>
            )}
          </div>
          <p className="text-[11px] text-muted-foreground">{t(($) => $.qa_review.live_idle_hint)}</p>
        </div>
        <Button type="button" size="sm" variant="outline" className="h-8 shrink-0 gap-1.5 text-[12px]" onClick={onOpen}>
          <Globe className="size-3.5" />
          {t(($) => $.qa_review.live_open)}
        </Button>
      </div>
    );
  }

  // Self-host: dial the daemon directly. Cloud: the same-origin proxied base.
  const daemonUrl =
    browser?.mode === "self-host"
      ? browser.daemon_url
      : browser?.mode === "cloud"
        ? browser.browser_url
        : "";
  const previewUrl = preview?.url ?? "";

  // A deployed QA target + a reachable daemon → drive it in a CDP Chromium.
  // Auto-streams: the box is exactly what the QA engineer came to see, and the
  // per-URL warm Chromium makes repeat opens instant.
  const boxBrowse = !!daemonUrl && !!previewUrl;
  // No daemon at all → the target can only open in a real tab.
  const linkOnly = !browserLoading && !boxBrowse && !!previewUrl;

  // Opening the QA review is explicit intent to watch the configured target,
  // so the CDP connection may start immediately once both URLs are available.
  const live = boxBrowse;
  // Which environment is under test, surfaced as a chip so neither the reviewer
  // nor the QA agent is ever unsure: the configured QA target's host.
  const envLabel = boxBrowse
    ? (() => {
        try {
          return new URL(previewUrl).host;
        } catch {
          return previewUrl;
        }
      })()
    : "";

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border bg-card">
      <div className="flex items-center gap-2 border-b px-3 py-2">
        <Globe className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="text-[12px] font-medium">{t(($) => $.qa_review.live_testing)}</span>
        {envLabel && (
          <span
            className="max-w-[170px] truncate rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground"
            title={envLabel}
          >
            {envLabel}
          </span>
        )}
        <div className="ml-auto flex items-center gap-2">
          {live && (
            <span className="flex items-center gap-1.5 text-[10px] uppercase tracking-wide text-muted-foreground">
              <span aria-hidden className="size-1.5 rounded-full bg-emerald-500 motion-safe:animate-pulse" />
              {t(($) => $.qa_review.live_on)}
            </span>
          )}
          {/* Collapse — put the bay away without waiting for the run to end.
              Sticky for this mount (QALensBody won't reopen it while this
              same run keeps going); reopening is always one click away via
              the compact card's "Open live testing" button. */}
          <button
            type="button"
            onClick={onCollapse}
            title={t(($) => $.qa_review.live_collapse)}
            aria-label={t(($) => $.qa_review.live_collapse)}
            className="shrink-0 rounded-md p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <X className="size-3.5" />
          </button>
        </div>
      </div>

      {boxBrowse ? (
        // Deployed QA target, driven in CDP. Keyed by the target URL so the
        // warm Chromium is shared across every issue that QA's this box.
        <div className="min-h-0 flex-1">
          <EditorBrowserPane daemonUrl={daemonUrl} workdir={`qa-target:${previewUrl}`} initialUrl={previewUrl} />
        </div>
      ) : linkOnly ? (
        // No reachable daemon to drive a Chromium — hand the target off to a
        // real browser tab (never an iframe: the box's CSP would blank it).
        <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-10 text-center">
          <span className="flex size-11 items-center justify-center rounded-full bg-muted">
            <Globe className="size-5 text-muted-foreground" aria-hidden />
          </span>
          <p className="max-w-[260px] text-[11px] leading-relaxed text-muted-foreground">
            {t(($) => $.qa_review.live_preview_open_tab)}
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
          {browserLoading || previewLoading ? (
            <div className="flex items-center gap-1.5 text-[12px] text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" />
              {t(($) => $.qa_review.live_browser_loading)}
            </div>
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
