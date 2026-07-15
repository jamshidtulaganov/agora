/** JSON headers for a daemon POST. Same-origin cloud proxy requests need the
 * Agora CSRF token; direct localhost daemon requests must not receive it. */
export function daemonProxyHeaders(daemonUrl: string): Record<string, string> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (/^https?:\/\//.test(daemonUrl)) return headers;
  if (typeof document !== "undefined") {
    const match = document.cookie
      .split("; ")
      .find((cookie) => cookie.startsWith("agora_csrf="));
    const token = match?.split("=")[1];
    if (token) headers["X-CSRF-Token"] = token;
  }
  return headers;
}

/** Resolves a direct daemon URL or a same-origin cloud proxy base. */
export function absoluteDaemonBase(daemonUrl: string): string {
  if (/^https?:\/\//.test(daemonUrl)) return daemonUrl;
  if (typeof window === "undefined") return daemonUrl;
  return window.location.origin + daemonUrl;
}
