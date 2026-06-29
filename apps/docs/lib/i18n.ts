import { defineI18n } from "fumadocs-core/i18n";

// English is the default; Chinese (/zh/), Russian (/ru/), and Uzbek (/uz/) are
// available. hideLocale: 'default-locale' keeps English URLs prefix-free
// (`/docs/`) while translated locales live under `/docs/<lang>/...`. parser:
// 'dot' picks up `page.<lang>.mdx` and `meta.<lang>.json`. A page with no file
// for a given locale falls back to English under that locale's prefix.
export const i18n = defineI18n({
  languages: ["en", "zh", "ru", "uz"],
  defaultLanguage: "en",
  hideLocale: "default-locale",
  parser: "dot",
});

export type Lang = (typeof i18n.languages)[number];
