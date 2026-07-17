import { useConfigStore } from "@agora/core/config";

/**
 * Origin for absolute URLs that leave the app — e.g. an invite link pasted
 * into Telegram. On web the page runs same-origin with the API, so the
 * current origin IS the shareable one (unchanged behavior). The desktop
 * renderer is served from file:// (packaged builds), which would produce
 * `file:///invite/<id>` — there we fall back to the server-advertised app
 * URL from /api/config (`daemon_app_url`), loaded into the config store at
 * boot on both platforms.
 *
 * Returns "" when neither source is available (desktop before /api/config
 * resolves); callers should treat that as "link not ready yet".
 */
export function useShareableOrigin(): string {
  const daemonAppUrl = useConfigStore((s) => s.daemonAppUrl);
  if (
    typeof window !== "undefined" &&
    (window.location.protocol === "http:" ||
      window.location.protocol === "https:")
  ) {
    return window.location.origin;
  }
  return daemonAppUrl.replace(/\/+$/, "");
}
