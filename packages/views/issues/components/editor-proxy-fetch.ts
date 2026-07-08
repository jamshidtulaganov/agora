// Shared fetch helpers for the co-code editor panes (preview, changes, review
// bar, browser). A pane's `daemonUrl` is EITHER a direct self-host daemon URL
// (http://127.0.0.1:<port>) OR a same-origin cloud proxy base
// (/browser/proxy/<token>). The two need different request shaping:
//
//   - Self-host (absolute http URL): the browser calls the daemon directly.
//     The daemon's CORS allowlist is Content-Type only and it does its own
//     localhost-origin check, so NO Agora CSRF header may ride along.
//   - Cloud (path base): the request goes through the backend's authed session
//     (/browser/proxy is behind the cookie session), which enforces the
//     double-submit CSRF check on POSTs — so the X-CSRF-Token cookie value MUST
//     ride along, or every POST is rejected 403. Cookies are sent automatically
//     (same-origin fetch), but the CSRF header is not.
//
// These were originally local to editor-browser-pane; extracted so the preview,
// changes, and review-bar panes (written for self-host) work in cloud too.

/** JSON headers for a pane POST, adding the CSRF token only on the cloud path. */
export function proxyHeaders(daemonUrl: string): Record<string, string> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (/^https?:\/\//.test(daemonUrl)) return headers; // self-host: no CSRF
  if (typeof document !== "undefined") {
    const match = document.cookie
      .split("; ")
      .find((c) => c.startsWith("agora_csrf="));
    const token = match?.split("=")[1];
    if (token) headers["X-CSRF-Token"] = token;
  }
  return headers;
}

/** Absolute base for a fetch: a cloud path base is prefixed with the page
 * origin so the URL is well-formed; an absolute self-host URL passes through. */
export function absoluteBase(daemonUrl: string): string {
  if (/^https?:\/\//.test(daemonUrl)) return daemonUrl;
  if (typeof window === "undefined") return daemonUrl;
  return window.location.origin + daemonUrl;
}
