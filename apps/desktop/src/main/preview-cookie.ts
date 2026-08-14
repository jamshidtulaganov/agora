type ResponseHeaders = Record<string, string[]>;

const LOCAL_PREVIEW_PATH = /^\/editor\/local\/\d+(?:\/|$)/;

export function isLocalPreviewResponse(rawURL: string): boolean {
  try {
    const url = new URL(rawURL);
    return (
      (url.hostname === "127.0.0.1" || url.hostname === "localhost") &&
      LOCAL_PREVIEW_PATH.test(url.pathname)
    );
  } catch {
    return false;
  }
}

/**
 * Local product previews are third-party frames inside the file:// desktop
 * renderer. Legacy apps commonly emit a session cookie without SameSite,
 * which Chromium treats as Lax and therefore drops in that frame. Make only
 * those local preview cookies explicitly cross-site compatible.
 */
export function rewriteLocalPreviewCookies(
  rawURL: string,
  headers: ResponseHeaders | undefined,
): ResponseHeaders | undefined {
  if (!headers || !isLocalPreviewResponse(rawURL)) return headers;

  let changed = false;
  const rewritten = { ...headers };
  for (const [name, values] of Object.entries(headers)) {
    if (name.toLowerCase() !== "set-cookie") continue;
    rewritten[name] = values.map((value) => {
      if (/;\s*SameSite=/i.test(value)) return value;
      changed = true;
      const secure = /;\s*Secure(?:;|$)/i.test(value) ? value : `${value}; Secure`;
      return `${secure}; SameSite=None`;
    });
  }

  return changed ? rewritten : headers;
}
