// The Mini App is served from its own host (sd-agora-tg) which reverse-proxies
// /api and /uploads to the PRIVATE backend over Fly 6PN. So any Agora-hosted
// file URL must load SAME-ORIGIN — an absolute URL pointing at the backend's
// own (unreachable) host, or at another deploy domain, would 404 / hang in the
// Telegram webview and render as a broken image.
//
// This rewrites Agora attachment/upload URLs to a bare same-origin path so the
// proxy serves them. External URLs (e.g. S3/CloudFront signed links, Bitrix)
// are left untouched — they're meant to load cross-origin.
export function sameOriginFileUrl(src: string | undefined | null): string {
  if (!src) return "";
  // Already a same-origin relative path.
  if (src.startsWith("/")) return src;
  try {
    const u = new URL(src);
    if (
      u.pathname.startsWith("/api/attachments") ||
      u.pathname.startsWith("/api/files") ||
      u.pathname.startsWith("/uploads/")
    ) {
      return u.pathname + u.search;
    }
    return src;
  } catch {
    return src;
  }
}

export function isImage(contentType: string): boolean {
  return contentType.startsWith("image/");
}

export function isVideo(contentType: string): boolean {
  return contentType.startsWith("video/");
}

export function humanSize(bytes: number): string {
  if (!bytes || bytes < 1024) return `${bytes || 0} B`;
  const kb = bytes / 1024;
  if (kb < 1024) return `${Math.round(kb)} KB`;
  return `${(kb / 1024).toFixed(1)} MB`;
}
