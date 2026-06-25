import type { SupportedLocale } from "@agora/core/i18n";

export function docsHrefForLocale(locale: SupportedLocale): string {
  if (locale === "zh-Hans") return "/docs/zh";
  return "/docs";
}
