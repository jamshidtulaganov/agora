// Pure Figma-link extraction — the TS twin of server/internal/figma/links.go.
// Used client-side (e.g. the issue-detail "Figma design linked" chip) so the
// UI can detect referenced designs in any issue description without a server
// round-trip, against any server version. Keep the regex and normalization in
// lockstep with the Go primitive.

const FIGMA_URL_RE =
  /https:\/\/(?:www\.)?figma\.com\/(?:file|design|proto)\/([A-Za-z0-9]{10,})[^\s)\]"'<>]*/g;

export interface FigmaRef {
  url: string;
  file_key: string;
  node_id: string;
}

/** Normalizes the URL node-id form ("208-5147", possibly %3A-encoded) to the API form ("208:5147"). */
function nodeIdFromUrl(raw: string): string {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return "";
  }
  const nodeId = url.searchParams.get("node-id");
  if (!nodeId) return "";
  return nodeId.replaceAll("-", ":");
}

/**
 * Extracts every Figma design reference from free text, deduplicated by
 * (file key, node id). Pure — mirrors `figma.RefsFrom` on the server.
 */
export function figmaRefsFrom(text: string): FigmaRef[] {
  if (!text.includes("figma.com/")) return [];
  const out: FigmaRef[] = [];
  const seen = new Set<string>();
  for (const m of text.matchAll(FIGMA_URL_RE)) {
    const ref: FigmaRef = {
      url: m[0],
      file_key: m[1] ?? "",
      node_id: nodeIdFromUrl(m[0]),
    };
    const key = `${ref.file_key}|${ref.node_id}`;
    if (!ref.file_key || seen.has(key)) continue;
    seen.add(key);
    out.push(ref);
  }
  return out;
}
