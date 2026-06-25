import { useMemo } from "react";
import { getLocale } from "./locale";
import { STRINGS } from "./strings";

export { getLocale } from "./locale";
export type { Locale } from "./locale";

type Vars = Record<string, string | number>;

/** Translate a dotted key for the active locale, interpolating {var} placeholders.
 *  Falls back to English, then the raw key. */
export function useT() {
  const locale = getLocale();
  return useMemo(() => {
    return (key: string, vars?: Vars): string => {
      let s = STRINGS[locale]?.[key] ?? STRINGS.en[key] ?? key;
      if (vars) {
        for (const k of Object.keys(vars)) {
          s = s.replace(new RegExp(`\\{${k}\\}`, "g"), String(vars[k]));
        }
      }
      return s;
    };
  }, [locale]);
}

/** Localized compact relative time ("2 ч", "5 kun", else a short date). */
export function useFormatRelative() {
  const t = useT();
  const locale = getLocale();
  return useMemo(() => {
    return (iso: string): string => {
      const then = new Date(iso).getTime();
      if (Number.isNaN(then)) return "";
      const sec = Math.floor((Date.now() - then) / 1000);
      if (sec < 60) return t("time.now");
      const min = Math.floor(sec / 60);
      if (min < 60) return t("time.min", { n: min });
      const hr = Math.floor(min / 60);
      if (hr < 24) return t("time.hr", { n: hr });
      const day = Math.floor(hr / 24);
      if (day < 7) return t("time.day", { n: day });
      return new Date(then).toLocaleDateString(locale === "en" ? undefined : locale, {
        month: "short",
        day: "numeric",
      });
    };
  }, [t, locale]);
}
