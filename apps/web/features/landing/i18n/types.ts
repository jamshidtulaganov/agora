import type { SupportedLocale } from "@agora/core/i18n";
export { docsHrefForLocale } from "@/lib/docs-href";

export type Locale = SupportedLocale;
export type LandingDictionaryLocale = "en" | "zh" | "uz" | "ru";

// Mirrors SUPPORTED_LOCALES (zh-Hans temporarily disabled).
export const locales: Locale[] = ["en", "uz", "ru"];

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

// The six departments the landing shows. Keys are locale-invariant; names
// and agent names come from the dictionary.
export type LandingDeptKey =
  | "sales"
  | "support"
  | "hr"
  | "finance"
  | "operations"
  | "engineering";

type TitledText = { title: string; body: string };

export type ContactSalesOption = { value: string; label: string };

export type LandingDict = {
  header: {
    cta: string;
    login: string;
    dashboard: string;
    docs: string;
    changelog: string;
    whatIs: string;
    howItWorks: string;
    useCases: string;
    guide: string;
    pricing: string;
    download: string;
    contactSales: string;
    navigation: string;
    openMenu: string;
    closeMenu: string;
    language: string;
  };
  depts: Record<LandingDeptKey, { name: string; agent: string }>;
  hero: {
    badge: string;
    titleLead: string;
    titleAccent: string;
    subheading: string;
    cta: string;
    talkToSales: string;
    board: {
      workspace: string;
      allTasks: string;
      columns: { todo: string; inProgress: string; inReview: string; done: string };
      cards: {
        monthEnd: string;
        hiring: string;
        invoices: string;
        refunds: string;
        onboarding: string;
        weekly: string;
      };
      needsApproval: string;
      liveTitle: string;
      // Four stages of the animated card: assigned, working, review, approved.
      liveNotes: [string, string, string, string];
    };
  };
  what: {
    label: string;
    title: string;
    points: [TitledText, TitledText, TitledText];
    picker: { assignTo: string; people: string; agents: string; selected: string };
  };
  how: {
    label: string;
    title: string;
    subtitle: string;
    tasksThisWeek: string;
    proof: string;
    source: string;
    stepDone: string;
    stepOpen: string;
    approve: string;
    askForChanges: string;
    // Exactly four tasks; the component pairs them with fixed assignee data.
    tasks: [HowTask, HowTask, HowTask, HowTask];
  };
  agentSetup: {
    label: string;
    title: string;
    steps: [TitledText, TitledText, TitledText];
    card: {
      title: string;
      nameLabel: string;
      nameValue: string;
      jobLabel: string;
      jobValue: string;
      docsLabel: string;
      docs: string[];
      toolsLabel: string;
      createAgent: string;
    };
  };
  automation: {
    label: string;
    title: string;
    body: string;
    cardTitle: string;
    when: string;
    if: string;
    then: string;
    whenValue: string;
    ifValue: string;
    thenValue: string;
    footnote: string;
    moreExamples: string;
    examples: string[];
  };
  departments: {
    label: string;
    title: string;
    body: string;
    youAsk: string;
    /** `{agent}` is replaced with the department's agent name. */
    agentDoes: string;
    youGet: string;
    setupOnce: string;
    seeAll: string;
    moreTasks: string;
    teams: Record<
      LandingDeptKey,
      { ask: string; does: string; get: string; setup: string; tasks: string[] }
    >;
  };
  inside: {
    label: string;
    title: string;
    knowledge: TitledText & { files: string[]; quote: string; quoteSource: string };
    assistant: TitledText & { userMsg: string; reply: string };
    automations: TitledText;
  };
  control: {
    label: string;
    title: string;
    body: string;
    items: TitledText[];
  };
  tools: {
    title: string;
    body: string;
    anyMcp: string;
  };
  pricing: {
    label: string;
    title: string;
    subtitle: string;
    free: {
      name: string;
      description: string;
      price: string;
      period: string;
      features: string[];
      cta: string;
    };
    team: {
      name: string;
      badge: string;
      description: string;
      price: string;
      period: string;
      monthly: string;
      features: string[];
      cta: string;
    };
    trialNote: string;
    needMore: string;
    contactSales: string;
  };
  faq: {
    label: string;
    headline: string;
    items: { question: string; answer: string }[];
  };
  finalCta: {
    title: string;
    body: string;
    cta: string;
    talkToSales: string;
  };
  footer: {
    navigation: string;
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
      // Shown to macOS visitors whenever a mac build is offered. The builds
      // are not notarized by Apple, so Gatekeeper blocks the first launch
      // outright — without these steps the download is unusable.
      macGatekeeper: { title: string; body: string };
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
      installUnixLabel: string;
      installWindowsLabel: string;
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

type HowTask = {
  title: string;
  status: string;
  steps: [string, string, string];
  proof: string;
  source: string;
};
