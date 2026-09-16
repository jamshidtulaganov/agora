// Pure helpers for rendering a `role: "tool"` transcript row as a compact
// action chip. `tool_result` is arbitrary, tool-specific JSON (see
// server/internal/assistant/tools.go's catalog) — these heuristics degrade
// gracefully on an unrecognized shape rather than throwing, per the API
// Response Compatibility rule (enum/shape drift downgrades, never crashes).

/** "search_issues" -> "search issues" — generic, works for any future tool
 *  name without needing a per-tool translation entry. */
export function humanizeToolName(name: string): string {
  return name.replace(/_/g, " ").trim();
}

/** Fields checked, in priority order, for an obvious one-line summary. */
const SUMMARY_FIELDS = ["title", "name", "issue_key", "key", "id", "label", "url"] as const;

const MAX_SUMMARY_LENGTH = 140;

function truncate(value: string): string {
  return value.length > MAX_SUMMARY_LENGTH
    ? value.slice(0, MAX_SUMMARY_LENGTH).trimEnd() + "…"
    : value;
}

/** True when this tool_result is the server's `{"error": "..."}` shape. */
export function isToolResultError(result: unknown): result is { error: string } {
  return (
    !!result &&
    typeof result === "object" &&
    typeof (result as Record<string, unknown>).error === "string" &&
    (result as Record<string, unknown>).error !== ""
  );
}

/**
 * Best-effort one-line summary of a tool result for the action chip.
 * Returns null when nothing usable is found — the chip then shows just the
 * icon + humanized tool name.
 */
export function summarizeToolResult(result: unknown): string | null {
  if (result == null) return null;
  if (typeof result === "string") {
    return result.trim() ? truncate(result.trim()) : null;
  }
  if (typeof result !== "object") return String(result);

  const obj = result as Record<string, unknown>;

  if (isToolResultError(obj)) return truncate(obj.error);

  for (const field of SUMMARY_FIELDS) {
    const value = obj[field];
    if (typeof value === "string" && value.trim()) return truncate(value.trim());
  }

  // Common list-shaped results: { issues: [...] }, { workspaces: [...] }, etc.
  for (const value of Object.values(obj)) {
    if (Array.isArray(value)) return `${value.length}`;
  }
  if (typeof obj.count === "number") return String(obj.count);

  return null;
}
