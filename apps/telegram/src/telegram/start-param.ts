// Encode an "open issue <id>" deep-link payload, mirroring the backend's
// telegram.MiniAppStartParam (base64url, no padding, of "i:<issueID>"). Used to
// build t.me/<bot>?startapp=<param> share links that land the recipient on the
// issue.
export function encodeStartParam(issueId: string): string {
  return btoa("i:" + issueId)
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

export interface StartTarget {
  /** Workspace slug to switch to before opening the issue (newer links). */
  wsSlug: string | null;
  /** Issue id (UUID) to open on launch. */
  issueId: string | null;
}

// Decode the Mini App deep-link payload (startapp=...) produced by the backend's
// telegram.MiniAppStartParam: base64url(no-pad) of "i:<wsSlug>:<issueID>" (newer)
// or "i:<issueID>" (legacy). Returns the workspace + issue to open, or nulls
// when there is no (valid) payload.
export function decodeStartParam(param: string | null): StartTarget {
  if (!param) return { wsSlug: null, issueId: null };
  try {
    const b64 = param.replace(/-/g, "+").replace(/_/g, "/");
    const padded = b64 + "===".slice((b64.length + 3) % 4);
    const decoded = atob(padded);
    if (decoded.startsWith("i:")) {
      const rest = decoded.slice(2);
      const sep = rest.indexOf(":");
      if (sep >= 0) {
        // "i:<wsSlug>:<issueId>" — slugs and UUIDs contain no ":", so the first
        // colon cleanly separates them.
        return { wsSlug: rest.slice(0, sep) || null, issueId: rest.slice(sep + 1) || null };
      }
      return { wsSlug: null, issueId: rest || null };
    }
  } catch {
    // malformed payload — ignore and open the default tab
  }
  return { wsSlug: null, issueId: null };
}
