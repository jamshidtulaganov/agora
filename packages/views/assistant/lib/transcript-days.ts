// Day boundaries inside a transcript. Pure, and deliberately LOCAL-time: a
// conversation is split where the reader's own day changed, not where UTC
// rolled over.

/** `2026-9-16` in the reader's timezone; null when the stamp is unusable. */
export function dayKey(iso: string | null | undefined): string | null {
  if (!iso) return null;
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return null;
  return `${date.getFullYear()}-${date.getMonth() + 1}-${date.getDate()}`;
}

/**
 * True when a divider belongs between these two rows.
 *
 * The first row of a transcript gets NO divider — the top of a conversation
 * is already an obvious boundary, and a date banner there is chrome. A row
 * with an unreadable timestamp never introduces one either.
 */
export function isDayBoundary(
  previousIso: string | null | undefined,
  currentIso: string | null | undefined,
): boolean {
  const current = dayKey(currentIso);
  if (!current) return false;
  const previous = dayKey(previousIso);
  if (!previous) return false;
  return previous !== current;
}

export type RelativeDay = "today" | "yesterday" | "older";

/** Which label the divider should use, relative to `now`. */
export function relativeDay(iso: string, now: Date = new Date()): RelativeDay {
  const key = dayKey(iso);
  if (!key) return "older";
  if (key === dayKey(now.toISOString())) return "today";
  const yesterday = new Date(now.getTime());
  yesterday.setDate(yesterday.getDate() - 1);
  if (key === dayKey(yesterday.toISOString())) return "yesterday";
  return "older";
}
