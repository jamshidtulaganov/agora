/* eslint-disable i18next/no-literal-string -- low-level QA browser controls */
"use client";

import { useState, useEffect, useRef, useCallback } from "react";
import { RotateCw, Loader2, TriangleAlert, Globe, ChevronUp, ChevronDown, Trash2 } from "lucide-react";
import { Button } from "@agora/ui/components/ui/button";
import { Input } from "@agora/ui/components/ui/input";
import { daemonProxyHeaders as proxyHeaders, absoluteDaemonBase as absoluteBase } from "./daemon-proxy-fetch";

// Embedded browser ("general browser pane"). Opens a WebSocket to the daemon,
// which screencasts a headless Chromium (CDP) — frames render here, and mouse /
// keyboard / navigation are forwarded back, so it's an interactive browser
// inside the QA surface. It loads the configured QA target, and the
// same Chromium is what an automation script attaches to via
// `connectOverCDP(<cdp_url>)` — so you watch the automation run. Self-host only.

// The daemon pins the Chromium viewport to 1280×800; clicks map by ratio.
const FRAME_W = 1280;
const FRAME_H = 800;

type StreamState = "connecting" | "live" | "error" | "closed";

// One row in the inspector strip — a console error/warning or a failed /
// 4xx/5xx network request, streamed live from the daemon's CDP bridge.
type InspectorEvent = {
  kind: "console" | "network";
  level: "error" | "warning";
  text: string;
  status?: number;
};

const MAX_INSPECTOR_EVENTS = 200;

// A daemonUrl may be absolute (self-host: http://127.0.0.1:<port>) or a
// same-origin path base (cloud: /browser/proxy/<token> — the backend
// reverse-proxies to the remote daemon). Normalize to absolute so the
// http→ws scheme swap for the stream URL works in both shapes.
// proxyHeaders / absoluteBase live in daemon-proxy-fetch (shared with the
// artifact runtime panels, which need the same cloud-vs-self-host request shaping).

export function EditorBrowserPane({
  daemonUrl,
  workdir,
  initialUrl,
}: {
  daemonUrl: string;
  workdir: string;
  // The browser navigates straight here on connect. Used by QA to point the CDP
  // Chromium at a deployed target (e.g. agora.sdteam.uz) that a plain iframe
  // can't embed (its CSP frame-ancestors blanks cross-origin embeds; a real
  // top-level navigation isn't subject to it).
  initialUrl: string;
}) {
  const [address, setAddress] = useState(initialUrl);
  const [state, setState] = useState<StreamState>("connecting");
  const [err, setErr] = useState("");
  const [frame, setFrame] = useState("");
  const [nonce, setNonce] = useState(0);
  const [hasNavigated, setHasNavigated] = useState(false);
  const [events, setEvents] = useState<InspectorEvent[]>([]);
  const [inspectorOpen, setInspectorOpen] = useState(false);
  // The strip auto-expands when the first error lands (a tester shouldn't
  // have to notice a tiny badge), but never re-opens against an explicit
  // collapse — the ref remembers the user's choice for this connection.
  const userCollapsedRef = useRef(false);
  const wsRef = useRef<WebSocket | null>(null);
  const imgRef = useRef<HTMLImageElement | null>(null);
  // Latest frame's object URL. Frames arrive as binary JPEG Blobs; each becomes
  // an object URL for the <img>. Held in a ref so the previous URL is revoked
  // when the next frame lands (and on teardown) — otherwise every frame leaks a
  // blob for the tab's lifetime.
  const frameUrlRef = useRef("");

  const send = useCallback((obj: unknown) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(obj));
  }, []);

  useEffect(() => {
    let closed = false;
    const base = absoluteBase(daemonUrl);
    setState("connecting");
    setErr("");
    setEvents([]);
    setInspectorOpen(false);
    userCollapsedRef.current = false;
    void (async () => {
      try {
        const r = await fetch(`${base}/editor/browser/start`, {
          method: "POST",
          headers: proxyHeaders(daemonUrl),
          body: JSON.stringify({ workdir }),
        });
        const d = (await r.json()) as { error?: string; cdp_url?: string };
        if (closed) return;
        if (d.error) {
          setState("error");
          setErr(d.error);
          return;
        }
      } catch {
        if (!closed) {
          setState("error");
          setErr("daemon unreachable");
        }
        return;
      }
      const wsUrl =
        base.replace(/^http/, "ws") +
        `/editor/browser/stream?workdir=${encodeURIComponent(workdir)}`;
      const ws = new WebSocket(wsUrl);
      wsRef.current = ws;
      ws.onopen = () => {
        if (closed) return;
        setState("live");
        // With an initialUrl (QA: point straight at the deployed target),
        // navigate there on connect. Otherwise blank the page so we never
        // flash Chromium's startup / error page before the user navigates.
        const target = initialUrl?.trim();
        if (target) {
          setHasNavigated(true);
          ws.send(JSON.stringify({ type: "navigate", url: target }));
        } else {
          ws.send(JSON.stringify({ type: "navigate", url: "about:blank" }));
        }
      };
      ws.binaryType = "blob";
      ws.onmessage = (ev) => {
        // Binary payload = a JPEG screencast frame. Turn it into an object URL,
        // revoking the previous one so blobs don't accumulate.
        if (typeof ev.data !== "string") {
          const url = URL.createObjectURL(ev.data as Blob);
          if (frameUrlRef.current) URL.revokeObjectURL(frameUrlRef.current);
          frameUrlRef.current = url;
          setFrame(url);
          return;
        }
        // Text payload = a JSON control message: bridge errors, or live
        // console/network events for the inspector strip.
        try {
          const m = JSON.parse(ev.data) as {
            type: string;
            message?: string;
            level?: string;
            text?: string;
            status?: number;
          };
          if (m.type === "error") {
            setState("error");
            setErr(m.message || "browser error");
          } else if (m.type === "console" || m.type === "network") {
            const item: InspectorEvent = {
              kind: m.type,
              level: m.level === "warning" ? "warning" : "error",
              text: m.text ?? "",
              status: typeof m.status === "number" && m.status > 0 ? m.status : undefined,
            };
            setEvents((prev) => {
              const next = prev.length >= MAX_INSPECTOR_EVENTS ? prev.slice(1) : prev.slice();
              next.push(item);
              return next;
            });
            if (!userCollapsedRef.current) setInspectorOpen(true);
          }
        } catch {
          /* ignore malformed */
        }
      };
      ws.onerror = () => {
        if (!closed) {
          setState("error");
          setErr("stream error");
        }
      };
      ws.onclose = () => {
        if (!closed) setState((s) => (s === "error" ? s : "closed"));
      };
    })();
    return () => {
      closed = true;
      wsRef.current?.close();
      wsRef.current = null;
      if (frameUrlRef.current) {
        URL.revokeObjectURL(frameUrlRef.current);
        frameUrlRef.current = "";
      }
    };
  }, [daemonUrl, workdir, nonce, initialUrl]);

  const toCdp = (e: React.MouseEvent) => {
    const img = imgRef.current;
    if (!img) return { x: 0, y: 0 };
    const rect = img.getBoundingClientRect();
    const x = ((e.clientX - rect.left) / rect.width) * FRAME_W;
    const y = ((e.clientY - rect.top) / rect.height) * FRAME_H;
    return {
      x: Math.max(0, Math.min(FRAME_W, x)),
      y: Math.max(0, Math.min(FRAME_H, y)),
    };
  };
  const btn = (e: React.MouseEvent) =>
    e.button === 2 ? "right" : e.button === 1 ? "middle" : "left";

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key.length === 1 && !e.ctrlKey && !e.metaKey) {
      e.preventDefault();
      send({ type: "key", cdpType: "char", text: e.key, key: e.key });
      return;
    }
    const map: Record<string, number> = {
      Enter: 13,
      Backspace: 8,
      Tab: 9,
      Delete: 46,
      Escape: 27,
      ArrowLeft: 37,
      ArrowUp: 38,
      ArrowRight: 39,
      ArrowDown: 40,
    };
    if (e.key in map) {
      e.preventDefault();
      send({
        type: "key",
        cdpType: "keyDown",
        key: e.key,
        code: e.code,
        keyCode: map[e.key],
        ...(e.key === "Enter" ? { text: "\r" } : {}),
      });
    }
  };

  const go = (e: React.FormEvent) => {
    e.preventDefault();
    let u = address.trim();
    if (!u) return;
    if (!/^https?:\/\//.test(u) && u !== "about:blank") u = "https://" + u;
    setAddress(u);
    setHasNavigated(true);
    send({ type: "navigate", url: u });
  };

  const consoleCount = events.filter((e) => e.kind === "console").length;
  const networkCount = events.filter((e) => e.kind === "network").length;

  return (
    <div className="flex min-w-0 flex-1 flex-col bg-background">
      <form
        onSubmit={go}
        className="flex shrink-0 items-center gap-1.5 border-b border-border px-2 py-1.5"
      >
        <Button
          type="button"
          variant="outline"
          size="icon-sm"
          onClick={() => send({ type: "reload" })}
          title="Reload"
          aria-label="Reload QA target"
        >
          <RotateCw className="h-3.5 w-3.5" />
        </Button>
        <Input
          value={address}
          onChange={(e) => setAddress(e.target.value)}
          placeholder="Enter a URL (e.g. localhost:3000) and press Enter"
          spellCheck={false}
          aria-label="QA target URL"
          className="min-w-0 flex-1 font-mono text-xs"
        />
        <Button type="submit" size="sm" className="shrink-0">
          Go
        </Button>
      </form>

      <div
        tabIndex={0}
        onKeyDown={onKey}
        aria-label="Interactive QA browser"
        className="relative min-h-0 flex-1 overflow-auto bg-neutral-900 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
      >
        {state === "live" && frame ? (
          <>
            <img
              ref={imgRef}
              src={frame}
              alt="QA target preview"
              draggable={false}
              onMouseMove={(e) => {
                const { x, y } = toCdp(e);
                send({ type: "mouse", cdpType: "mouseMoved", x, y });
              }}
              onMouseDown={(e) => {
                const { x, y } = toCdp(e);
                send({
                  type: "mouse",
                  cdpType: "mousePressed",
                  x,
                  y,
                  button: btn(e),
                  clickCount: 1,
                });
              }}
              onMouseUp={(e) => {
                const { x, y } = toCdp(e);
                send({
                  type: "mouse",
                  cdpType: "mouseReleased",
                  x,
                  y,
                  button: btn(e),
                  clickCount: 1,
                });
              }}
              onWheel={(e) => {
                const { x, y } = toCdp(e);
                send({ type: "wheel", x, y, deltaX: e.deltaX, deltaY: e.deltaY });
              }}
              onContextMenu={(e) => e.preventDefault()}
              className="w-full select-none"
            />
            {!hasNavigated ? (
              <div className="pointer-events-none absolute inset-0 flex items-center justify-center bg-background/85">
                <div className="pointer-events-auto flex flex-col items-center gap-2 px-6 text-center text-xs text-muted-foreground">
                  <Globe className="h-6 w-6" />
                  <span>Enter a QA target URL above.</span>
                </div>
              </div>
            ) : null}
          </>
        ) : (
          <div className="flex h-full items-center justify-center p-4 text-center text-xs text-muted-foreground">
            {state === "connecting" ? (
              <span className="inline-flex items-center gap-2">
                <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" />
                starting Chromium…
              </span>
            ) : state === "error" ? (
              <span className="inline-flex items-center gap-1 text-destructive">
                <TriangleAlert className="h-4 w-4" />
                {err}
              </span>
            ) : (
              <span className="inline-flex items-center gap-1">
                <Globe className="h-4 w-4" />
                browser closed —
                <button
                  type="button"
                  onClick={() => setNonce((n) => n + 1)}
                  className="rounded-sm underline outline-none hover:no-underline focus-visible:ring-2 focus-visible:ring-ring"
                >
                  reconnect
                </button>
              </span>
            )}
          </div>
        )}
      </div>

      {/* Inspector strip — live console errors/warnings + failed (4xx/5xx/
          hard-fail) network requests from the streamed Chromium, so a QA
          reviewer sees WHY a page is broken without opening devtools on a
          machine they can't reach. Collapsed by default; badge counts run
          even while collapsed. */}
      <div className="shrink-0 border-t border-border bg-background">
        <div className="flex items-center gap-2 px-2 py-1">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() =>
              setInspectorOpen((o) => {
                userCollapsedRef.current = o; // closing = explicit user choice
                return !o;
              })
            }
            className="h-6 gap-1.5 px-1.5 text-[10px] text-muted-foreground"
          >
            {inspectorOpen ? <ChevronDown className="h-3 w-3" /> : <ChevronUp className="h-3 w-3" />}
            Console
            <span className={consoleCount > 0 ? "text-destructive" : ""}>{consoleCount}</span>
            · Network
            <span className={networkCount > 0 ? "text-destructive" : ""}>{networkCount}</span>
          </Button>
          {events.length > 0 && (
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              onClick={() => setEvents([])}
              title="Clear"
              aria-label="Clear QA browser errors"
              className="ml-auto text-muted-foreground"
            >
              <Trash2 className="h-3 w-3" />
            </Button>
          )}
        </div>
        {inspectorOpen && (
          <div className="max-h-36 overflow-y-auto border-t border-border">
            {events.length === 0 ? (
              <p className="px-3 py-2 text-[10px] text-muted-foreground">
                No console errors or failed requests yet.
              </p>
            ) : (
              <ul className="divide-y divide-border/60">
                {events.map((e, i) => (
                  <li key={i} className="flex items-start gap-2 px-2 py-1 font-mono text-[10px] leading-relaxed">
                    <span
                      className={
                        "mt-0.5 shrink-0 rounded px-1 text-[9px] font-semibold uppercase " +
                        (e.level === "error"
                          ? "bg-destructive/10 text-destructive"
                          : "bg-amber-500/10 text-amber-600 dark:text-amber-400")
                      }
                    >
                      {e.kind === "network" ? (e.status ?? "ERR") : e.level === "error" ? "ERR" : "WARN"}
                    </span>
                    <span className="min-w-0 break-all text-foreground/90">{e.text}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
