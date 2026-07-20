/**
 * Synchronous, client-side OS detection for install-instruction surfaces.
 *
 * Only the OS family is needed (which install command to lead with), so a
 * plain user-agent check is enough — no async userAgentData round-trip like
 * the download page's arch-aware detector. Returns "unknown" outside a
 * browser (SSR, Node test env), which callers must treat as "don't reorder".
 */
export type DeviceOS = "mac" | "windows" | "linux" | "unknown";

export function detectDeviceOS(): DeviceOS {
  if (typeof navigator === "undefined") return "unknown";
  const ua = navigator.userAgent;
  if (/windows/i.test(ua)) return "windows";
  if (/mac os x|macintosh/i.test(ua)) return "mac";
  if (/linux|x11/i.test(ua)) return "linux";
  return "unknown";
}
