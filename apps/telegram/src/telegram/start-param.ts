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

// Decode the Mini App deep-link payload (startapp=...) produced by the backend's
// telegram.MiniAppStartParam: base64url(no-pad) of "i:<issueID>". Returns the
// issue id to open on launch, or null when there is no (valid) issue payload.
export function decodeStartParam(param: string | null): string | null {
  if (!param) return null;
  try {
    const b64 = param.replace(/-/g, "+").replace(/_/g, "/");
    const padded = b64 + "===".slice((b64.length + 3) % 4);
    const decoded = atob(padded);
    if (decoded.startsWith("i:")) {
      const id = decoded.slice(2);
      return id.length > 0 ? id : null;
    }
  } catch {
    // malformed payload — ignore and open the default tab
  }
  return null;
}
