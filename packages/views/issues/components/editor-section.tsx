"use client";

import { useState } from "react";
import { ChevronRight, ExternalLink, Loader2 } from "lucide-react";

// Right-panel "Code" section: launches a browser VS Code (code-server) pointed
// at the issue's agent worktree and iframes it, so a human can watch + edit the
// agent's live code. Flow (self-host): GET /api/issues/{id}/editor → {workdir,
// daemon_url}; then POST {daemon_url}/editor/launch {workdir} → {url} (the
// daemon, the browser and code-server are all on the same host, so the
// 127.0.0.1 url is directly iframe-able). Lazy: nothing runs until the user
// opens the section.
//
// NOTE (v1, self-host): the iframe is http://127.0.0.1:<port>. Works on the
// local http app; cloud/https will proxy code-server through the backend.

type LaunchState = "idle" | "loading" | "ready" | "none" | "error";

interface EditorSectionProps {
  issueId: string;
}

export function EditorSection({ issueId }: EditorSectionProps) {
  const [open, setOpen] = useState(false);
  const [state, setState] = useState<LaunchState>("idle");
  const [url, setUrl] = useState<string | null>(null);
  const [err, setErr] = useState("");

  const launch = async () => {
    setState("loading");
    setErr("");
    try {
      // The backend resolves the workspace from ?workspace_slug (the typed api
      // client sets it via a header; this plain fetch passes it explicitly).
      // The app routes under /{workspaceSlug}/… so it's the first path segment.
      const slug =
        typeof window !== "undefined"
          ? window.location.pathname.split("/").filter(Boolean)[0]
          : "";
      const r = await fetch(
        `/api/issues/${issueId}/editor${slug ? `?workspace_slug=${encodeURIComponent(slug)}` : ""}`,
        { credentials: "include" },
      );
      if (r.status === 404) {
        setState("none");
        return;
      }
      if (!r.ok) throw new Error(`editor lookup failed (${r.status})`);
      const data = (await r.json()) as {
        mode?: string;
        editor_url?: string;
        workdir?: string;
        daemon_url?: string;
        user_id?: string;
      };

      // Cloud: the backend already launched code-server on the remote daemon and
      // reverse-proxies it — iframe the same-origin url directly.
      if (data.mode === "cloud" && data.editor_url) {
        setUrl(data.editor_url);
        setState("ready");
        return;
      }

      // Self-host: the daemon is on this host — launch it from the browser.
      const lr = await fetch(`${data.daemon_url}/editor/launch`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ workdir: data.workdir, user_id: data.user_id }),
      });
      if (!lr.ok) {
        throw new Error(
          `daemon launch failed (${lr.status}) — is the daemon running?`,
        );
      }
      const { url: launched } = (await lr.json()) as { url: string };
      setUrl(launched);
      setState("ready");
    } catch (e) {
      setErr(e instanceof Error ? e.message : "failed to open editor");
      setState("error");
    }
  };

  const toggle = () => {
    const next = !open;
    setOpen(next);
    if (next && state === "idle") void launch();
  };

  return (
    <div>
      <button
        type="button"
        onClick={toggle}
        className={`mb-2 flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors hover:bg-accent/70 ${
          open ? "" : "text-muted-foreground hover:text-foreground"
        }`}
      >
        Code editor
        <ChevronRight
          className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${
            open ? "rotate-90" : ""
          }`}
        />
      </button>

      {open && (
        <div className="pl-2">
          {state === "loading" && (
            <div className="flex items-center gap-2 py-2 text-xs text-muted-foreground">
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
              launching VS Code…
            </div>
          )}

          {state === "none" && (
            <div className="py-2 text-xs text-muted-foreground">
              No worktree yet — assign an agent to this issue first.
            </div>
          )}

          {state === "error" && (
            <div className="py-2 text-xs text-destructive">
              {err}{" "}
              <button
                type="button"
                onClick={() => void launch()}
                className="underline hover:no-underline"
              >
                retry
              </button>
            </div>
          )}

          {state === "ready" && url && (
            <div className="space-y-1">
              <div className="flex justify-end">
                <a
                  href={url}
                  target="_blank"
                  rel="noreferrer noopener"
                  className="inline-flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-foreground"
                >
                  <ExternalLink className="h-3 w-3" />
                  open in tab
                </a>
              </div>
              <iframe
                src={url}
                title="code editor"
                className="h-[70vh] w-full rounded-lg border border-border bg-background"
              />
            </div>
          )}
        </div>
      )}
    </div>
  );
}
