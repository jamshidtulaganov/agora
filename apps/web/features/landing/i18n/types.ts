import type { SupportedLocale } from "@agora/core/i18n";
export { docsHrefForLocale } from "@/lib/docs-href";

export type Locale = SupportedLocale;
export type LandingDictionaryLocale = "en" | "zh" | "uz" | "ru";

export const locales: Locale[] = ["en", "zh-Hans", "uz", "ru"];

export const localeLabels: Record<Locale, string> = {
  en: "EN",
  "zh-Hans": "\u4e2d\u6587",
  uz: "O\u02bbzbekcha",
  ru: "\u0420\u0443\u0441\u0441\u043a\u0438\u0439",
};

export function toLandingDictionaryLocale(
  locale: Locale,
): LandingDictionaryLocale {
  if (locale === "uz") return "uz";
  if (locale === "ru") return "ru";
  return locale === "zh-Hans" ? "zh" : "en";
}

export function isZhLocale(locale: Locale): boolean {
  return locale === "zh-Hans";
}

type FeatureSection = {
  label: string;
  title: string;
  description: string;
  cards: { title: string; description: string }[];
};

// Strings rendered inside the coded product mockups (features section +
// autoplay demo). Member/agent names are NOT here — they are locale-invariant
// constants in the components.
type FeaturesMock = {
  issueTitle: string;
  issueDesc: string;
  activity: string;
  subscribe: string;
  properties: string;
  statusLabel: string;
  priorityLabel: string;
  assigneeLabel: string;
  assignTo: string;
  members: string;
  agents: string;
  unassigned: string;
  assignedToClaude: string;
  changedStatus: string;
  comment1: string;
  comment2: string;
  comment3: string;
  agentWorking: string;
  toolCalls: string;
  taskHistory: string;
  thinking1: string;
  thinking2: string;
  editResult: string;
  task1: string;
  task2: string;
  task3: string;
  skillsTitle: string;
  files: string;
  skill1Name: string;
  skill1Desc: string;
  skill2Name: string;
  skill2Desc: string;
  skill3Name: string;
  skill3Desc: string;
  skill4Name: string;
  skill4Desc: string;
  docTitle: string;
  docBody: string;
  steps: string;
  step1: string;
  step2: string;
  step3: string;
  step4: string;
  runtimesTitle: string;
  online: string;
  offline: string;
  justNow: string;
  hoursAgo: string;
  input: string;
  output: string;
  cacheRead: string;
  cacheWrite: string;
  activityCard: string;
  dailyCost: string;
  less: string;
  more: string;
  ccUserMsg: string;
  ccAgentReply: string;
  ccEditing: string;
  ccOpenEditor: string;
  qaPassed: string;
  qaCheck1: string;
  qaCheck2: string;
  qaCheck3: string;
  qaCheck4: string;
  qaCheck5: string;
  qaVerdict: string;
  qaPosted: string;
  qaShotCap: string;
  statuses: Record<
    "backlog" | "todo" | "in_progress" | "in_review" | "done",
    string
  >;
  priorities: Record<"none" | "low" | "medium" | "high" | "urgent", string>;
};

type FooterGroup = {
  label: string;
  links: { label: string; href: string }[];
};

export type ContactSalesOption = { value: string; label: string };

export type LandingDict = {
  header: {
    github: string;
    cta: string;
    dashboard: string;
    docs: string;
    changelog: string;
    useCases: string;
    guide: string;
    navigation: string;
    openMenu: string;
    closeMenu: string;
    language: string;
  };
  hero: {
    badge: string;
    headlineLine1: string;
    headlineLine2: string;
    subheading: string;
    cta: string;
    secondaryCta: string;
    downloadDesktop: string;
    talkToSales: string;
    worksWith: string;
    imageAlt: string;
  };
  features: {
    teammates: FeatureSection;
    autonomous: FeatureSection;
    cocode: FeatureSection;
    qa: FeatureSection;
    skills: FeatureSection;
    runtimes: FeatureSection;
    mock: FeaturesMock;
  };
  demo: {
    kicker: string;
    title: string;
    windowTitle: string;
    userMsg: string;
    agentReply: string;
    working: string;
    running: string;
    done: string;
    testsPassed: string;
    pr: string;
    readyToMerge: string;
    replyPlaceholder: string;
    statusLabel: string;
    assigneeLabel: string;
    priorityLabel: string;
    projectLabel: string;
    priorityHigh: string;
    project: string;
    statusTodo: string;
    statusInProgress: string;
    statusInReview: string;
  };
  howItWorks: {
    label: string;
    headlineMain: string;
    headlineFaded: string;
    steps: { title: string; description: string }[];
    cta: string;
    ctaGithub: string;
    ctaDocs: string;
  };
  openSource: {
    label: string;
    headlineLine1: string;
    headlineLine2: string;
    description: string;
    cta: string;
    highlights: { title: string; description: string }[];
  };
  faq: {
    label: string;
    headline: string;
    items: { question: string; answer: string }[];
  };
  footer: {
    tagline: string;
    cta: string;
    groups: {
      product: FooterGroup;
      resources: FooterGroup;
      company: FooterGroup;
    };
    copyright: string;
  };
  about: {
    title: string;
    nameLine: {
      prefix: string;
      mul: string;
      tiplexed: string;
      i: string;
      nformationAnd: string;
      c: string;
      omputing: string;
      a: string;
      gent: string;
    };
    paragraphs: string[];
    cta: string;
  };
  changelog: {
    title: string;
    subtitle: string;
    toc: string;
    categories: {
      features: string;
      improvements: string;
      fixes: string;
    };
    entries: {
      version: string;
      date: string;
      title: string;
      changes: string[];
      features?: string[];
      improvements?: string[];
      fixes?: string[];
    }[];
  };
  download: {
    hero: {
      macArm64: {
        title: string;
        sub: string;
        primary: string;
        altZip: string;
      };
      macIntel: {
        title: string;
        sub: string;
        disabledCta: string;
        intelHint: string;
      };
      winX64: { title: string; sub: string; primary: string };
      winArm64: { title: string; sub: string; primary: string };
      linux: {
        title: string;
        sub: string;
        primary: string;
        altFormats: string;
      };
      unknown: { title: string; sub: string };
      safariMacHint: string;
      archFallbackHint: string;
    };
    allPlatforms: {
      title: string;
      macLabel: string;
      winX64Label: string;
      winArm64Label: string;
      linuxX64Label: string;
      linuxArm64Label: string;
      formatDmg: string;
      formatZip: string;
      formatExe: string;
      formatAppImage: string;
      formatDeb: string;
      formatRpm: string;
      intelNote: string;
      unavailable: string;
    };
    cli: {
      title: string;
      sub: string;
      installLabel: string;
      startLabel: string;
      sshNote: string;
      copyLabel: string;
      copiedLabel: string;
    };
    cloud: { title: string; sub: string };
    footer: {
      releaseNotes: string;
      allReleases: string;
      currentVersion: string;
      versionUnavailable: string;
    };
  };
  contactSales: {
    pageTitle: string;
    pageDescription: string;
    eyebrow: string;
    title: string;
    subtitle: string;
    notice: { badge: string; body: string };
    fields: {
      firstName: string;
      lastName: string;
      businessEmail: string;
      businessEmailHint: string;
      companyName: string;
      companySize: string;
      countryRegion: string;
      useCase: string;
      goals: string;
      goalsHint: string;
      selectPlaceholder: string;
      submit: string;
      submitting: string;
    };
    companySizes: ContactSalesOption[];
    useCases: ContactSalesOption[];
    countries: string[];
    consent: {
      intro: string;
      outreach: string;
      updates: string;
      unsubscribe: string;
      submitConsent: string;
      privacyLinkLabel: string;
      privacyLinkHref: string;
    };
    success: { title: string; message: string; cta: string };
    errors: {
      generic: string;
      rateLimit: string;
      freeEmail: string;
      invalidEmail: string;
    };
  };
};
