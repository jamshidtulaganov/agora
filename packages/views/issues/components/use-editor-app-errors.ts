"use client";

import { useEffect, useState } from "react";
import { proxyHeaders, absoluteBase } from "./editor-proxy-fetch";

/**
 * useEditorAppErrors — the dev space's always-on "app health" watcher. Boots the
 * dev server (idempotent) and opens an EVENTS-ONLY browser stream
 * (`?events_only=1` — no JPEG screencast, so it doesn't spin the shared Chromium
 * on frames nobody renders) to count the running app's console errors + failed /
 * 4xx-5xx network requests. Lets the Watch / Preview panes surface "your app has
 * N errors" without the user being on the Browser tab.
 *
 * Requires the daemon to support `events_only` (a daemon redeploy). Degrades to
 * 0 on any failure — no daemon, a dev command that can't be auto-detected, a WS
 * error — and never throws into the UI. Resets the count on each (re)connect.
 */
export function useEditorAppErrors({
  daemonUrl,
  workdir,
  enabled,
}: {
  daemonUrl: string | undefined;
  workdir: string | undefined;
  enabled: boolean;
}): { errorCount: number } {
  const [errorCount, setErrorCount] = useState(0);

  useEffect(() => {
    setErrorCount(0);
    if (!enabled || !daemonUrl || !workdir) return;
    let closed = false;
    let ws: WebSocket | null = null;
    const base = absoluteBase(daemonUrl);

    void (async () => {
      // Idempotent: reuses an already-running dev server, else tries to detect +
      // start it. A `needs_command` / error response yields no url → we stay quiet.
      let url = "";
      try {
        const r = await fetch(`${base}/editor/preview`, {
          method: "POST",
          headers: proxyHeaders(daemonUrl),
          body: JSON.stringify({ workdir }),
        });
        const s = (await r.json()) as { url?: string };
        url = s.url ?? "";
      } catch {
        return;
      }
      if (closed || !url) return;

      try {
        ws = new WebSocket(
          base.replace(/^http/, "ws") +
            `/editor/browser/stream?workdir=${encodeURIComponent(workdir)}&events_only=1`,
        );
      } catch {
        return;
      }
      ws.onopen = () => {
        ws?.send(JSON.stringify({ type: "navigate", url }));
      };
      ws.onmessage = (ev) => {
        // events_only sends no binary frames; guard anyway. A "console" error or
        // any "network" event (the daemon only emits failed / 4xx-5xx) counts.
        if (typeof ev.data !== "string") return;
        try {
          const m = JSON.parse(ev.data) as { type?: string; level?: string };
          if ((m.type === "console" && m.level === "error") || m.type === "network") {
            setErrorCount((n) => n + 1);
          }
        } catch {
          /* ignore a malformed control frame */
        }
      };
    })();

    return () => {
      closed = true;
      ws?.close();
    };
  }, [daemonUrl, workdir, enabled]);

  return { errorCount };
}
