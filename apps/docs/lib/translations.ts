import type { Translations } from "fumadocs-ui/i18n";
import type { Lang } from "./i18n";

// Fumadocs built-in UI strings (search, TOC, last-updated, etc.) per locale.
// English uses Fumadocs defaults so we only override Chinese.
export const uiTranslations: Partial<Record<Lang, Partial<Translations>>> = {
  zh: {
    search: "搜索",
    searchNoResult: "没有找到结果",
    toc: "本页目录",
    tocNoHeadings: "无章节",
    lastUpdate: "最后更新于",
    chooseLanguage: "选择语言",
    nextPage: "下一页",
    previousPage: "上一页",
    chooseTheme: "切换主题",
    editOnGithub: "在 GitHub 上编辑",
  },
  ru: {
    search: "Поиск",
    searchNoResult: "Ничего не найдено",
    toc: "Содержание",
    tocNoHeadings: "Нет разделов",
    lastUpdate: "Последнее обновление",
    chooseLanguage: "Выберите язык",
    nextPage: "Следующая",
    previousPage: "Предыдущая",
    chooseTheme: "Сменить тему",
    editOnGithub: "Редактировать на GitHub",
  },
  uz: {
    search: "Qidirish",
    searchNoResult: "Hech narsa topilmadi",
    toc: "Mundarija",
    tocNoHeadings: "Boʻlimlar yoʻq",
    lastUpdate: "Oxirgi yangilanish",
    chooseLanguage: "Tilni tanlang",
    nextPage: "Keyingi",
    previousPage: "Oldingi",
    chooseTheme: "Mavzuni almashtirish",
    editOnGithub: "GitHub'da tahrirlash",
  },
};

// Display name shown in the LanguageToggle dropdown.
export const localeLabels: Record<Lang, string> = {
  en: "English",
  zh: "简体中文",
  ru: "Русский",
  uz: "Oʻzbekcha",
};

// Copy for the welcome page (Hero + Byline). Pages are translated as MDX;
// this dict only carries TSX-rendered chrome above the MDX body.
export const homeCopy = {
  en: {
    eyebrow: "Agora Docs",
    titleLead: "Humans and agents,",
    titleAccent: "in one place.",
    byline: ["Getting started", "Updated April 2026", "6 min read"],
  },
  zh: {
    eyebrow: "Agora 文档",
    titleLead: "人与智能体，",
    titleAccent: "共处一方。",
    byline: ["开始使用", "2026 年 4 月更新", "阅读约 6 分钟"],
  },
  ru: {
    eyebrow: "Документация Agora",
    titleLead: "Люди и агенты —",
    titleAccent: "в одном месте.",
    byline: ["Начало работы", "Обновлено в апреле 2026", "6 мин чтения"],
  },
  uz: {
    eyebrow: "Agora hujjatlari",
    titleLead: "Odamlar va agentlar —",
    titleAccent: "bir joyda.",
    byline: ["Boshlash", "2026-yil aprelda yangilangan", "6 daqiqa oʻqish"],
  },
} as const satisfies Record<Lang, unknown>;
