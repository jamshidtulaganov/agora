import type { SupportedLocale } from "@agora/core/i18n";
export { docsHrefForLocale } from "@/lib/docs-href";
import { getRequestLocale } from "@/lib/request-locale";

export const getUseCaseLocale = getRequestLocale;

type UseCaseText = {
  indexTitle: string;
  indexSubtitle: string;
  indexMetadataTitle: string;
  indexMetadataDescription: string;
  cardReadMore: string;
  tableOfContents: string;
};

export const useCaseText: Record<SupportedLocale, UseCaseText> = {
  en: {
    indexTitle: "Use cases",
    indexSubtitle:
      "See how teams organize people and agents together with Agora.",
    indexMetadataTitle: "Use cases",
    indexMetadataDescription:
      "See how teams put people and agents to work together with Agora.",
    cardReadMore: "Read →",
    tableOfContents: "On this page",
  },
  "zh-Hans": {
    indexTitle: "案例",
    indexSubtitle: "看看团队怎么用 Agora 把人和 agent 一起组织起来。",
    indexMetadataTitle: "案例",
    indexMetadataDescription:
      "看看团队怎么用 Agora 把人和 agent 一起组织起来。",
    cardReadMore: "阅读 →",
    tableOfContents: "目录",
  },
  uz: {
    indexTitle: "Foydalanish holatlari",
    indexSubtitle:
      "Jamoalar Agora bilan odamlar va agentlarni qanday birga tashkil qilishini ko‘ring.",
    indexMetadataTitle: "Foydalanish holatlari",
    indexMetadataDescription:
      "Jamoalar Agora bilan odamlar va agentlarni birga ishlatishini ko‘ring.",
    cardReadMore: "O‘qish →",
    tableOfContents: "Ushbu sahifada",
  },
  ru: {
    indexTitle: "Сценарии использования",
    indexSubtitle:
      "Узнайте, как команды объединяют людей и агентов с помощью Agora.",
    indexMetadataTitle: "Сценарии использования",
    indexMetadataDescription:
      "Узнайте, как команды заставляют людей и агентов работать вместе с Agora.",
    cardReadMore: "Читать →",
    tableOfContents: "На этой странице",
  },
};
