import { getLanguageCode } from "../telegram/sdk";

// The Mini App ships its own light-weight i18n (en/ru/uz) for its hand-written
// screens — separate from the shared @agora/views i18n. SalesDoctor's audience
// is Russian/Uzbek, so we default to Russian when the Telegram language is not
// one we ship.

export type Locale = "en" | "ru" | "uz";

let cached: Locale | null = null;

/** Resolve the active UI locale from the Telegram client language, memoized for
 *  the session (Telegram's language doesn't change at runtime). */
export function getLocale(): Locale {
  if (cached) return cached;
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
