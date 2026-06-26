import { getLanguageCode } from "../telegram/sdk";

// The Mini App ships its own light-weight i18n (en/ru/uz) for its hand-written
// screens — separate from the shared @agora/views i18n. SalesDoctor's audience
// is Russian/Uzbek, so we default to Russian when the Telegram language is not
// one we ship. A manual override (Settings → Language) wins over the Telegram
// language.

export type Locale = "en" | "ru" | "uz";

export const LOCALES: Locale[] = ["en", "ru", "uz"];

/** Native display names for the language picker. */
export const LOCALE_NAMES: Record<Locale, string> = {
  en: "English",
  ru: "Русский",
  uz: "O‘zbekcha",
};

const OVERRIDE_KEY = "tg_locale";

let cached: Locale | null = null;

function readOverride(): Locale | null {
  try {
    const v = localStorage.getItem(OVERRIDE_KEY);
    return v === "en" || v === "ru" || v === "uz" ? v : null;
  } catch {
    return null;
  }
}

/** Resolve the active UI locale: manual override → Telegram language → Russian.
 *  Memoized for the session (it only changes via a reload). */
export function getLocale(): Locale {
  if (cached) return cached;
  const override = readOverride();
  if (override) {
    cached = override;
    return cached;
  }
  const code = getLanguageCode().toLowerCase();
  cached = code.startsWith("ru")
    ? "ru"
    : code.startsWith("uz")
      ? "uz"
      : code.startsWith("en")
        ? "en"
        : "ru"; // SalesDoctor default audience
  return cached;
}

/** Persist a manual language choice and reload, so the new locale applies to
 *  both the local i18n and CoreProvider (both read the locale once at boot). */
export function setLocaleOverride(locale: Locale): void {
  try {
    localStorage.setItem(OVERRIDE_KEY, locale);
  } catch {
    /* ignore */
  }
  if (typeof window !== "undefined") window.location.reload();
}
