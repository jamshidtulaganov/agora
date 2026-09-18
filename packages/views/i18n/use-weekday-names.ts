import { useMemo } from "react";
import { useT } from "./use-t";

// 2026-02-01 is a Sunday, so `SUNDAY + day * DAY_MS` lands exactly on the day
// the scheduling contract numbers `day` (0=Sunday … 6=Saturday, JS getDay()).
const SUNDAY_UTC = Date.UTC(2026, 1, 1);
const DAY_MS = 86_400_000;

const FALLBACK_LONG = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
const FALLBACK_SHORT = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

/**
 * Localized day names indexed by JS `getDay()` (0=Sunday).
 *
 * Deliberately Intl, not locale JSON: day names are calendar data, not product
 * copy, and the only existing set in the bundles (autopilots' long names) is
 * owned by another namespace and has no short form. Intl gives both widths in
 * every locale the app ships without 7 * 4 * 2 new keys to keep in parity.
 *
 * Formatting runs in UTC against the reference week so a viewer east of the
 * line can't be shown the previous day's name.
 */
export function useWeekdayNames(width: "long" | "short" = "long"): string[] {
  const { i18n } = useT();
  const locale = i18n.language || "en";

  return useMemo(() => {
    try {
      const fmt = new Intl.DateTimeFormat(locale, { weekday: width, timeZone: "UTC" });
      return FALLBACK_LONG.map((_, day) => fmt.format(new Date(SUNDAY_UTC + day * DAY_MS)));
    } catch {
      // An unsupported locale tag must not take the schedule control down.
      return width === "short" ? FALLBACK_SHORT : FALLBACK_LONG;
    }
  }, [locale, width]);
}
