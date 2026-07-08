export type SupportedLocale = "en" | "zh-Hans" | "uz" | "ru";

// zh-Hans is temporarily disabled as a selectable locale (product decision).
// The type keeps the value so stored user.language="zh-Hans" and the dict
// files stay valid; re-enabling is adding it back to this list.
export const SUPPORTED_LOCALES: SupportedLocale[] = ["en", "uz", "ru"];
export const DEFAULT_LOCALE: SupportedLocale = "en";

export type LocaleResources = Record<string, Record<string, unknown>>;

export interface LocaleAdapter {
  getUserChoice(): string | null;
  getSystemPreferences(): string[];
  persist(locale: SupportedLocale): void;
}
