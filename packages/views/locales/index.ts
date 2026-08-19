import type { LocaleResources, SupportedLocale } from "@agora/core/i18n";
import enCommon from "./en/common.json";
import enAuth from "./en/auth.json";
import enSettings from "./en/settings.json";
import enIssues from "./en/issues.json";
import enAgents from "./en/agents.json";
import enEditor from "./en/editor.json";
import enOnboarding from "./en/onboarding.json";
import enInvite from "./en/invite.json";
import enLabels from "./en/labels.json";
import enMembers from "./en/members.json";
import enMyIssues from "./en/my-issues.json";
import enSearch from "./en/search.json";
import enInbox from "./en/inbox.json";
import enWorkspace from "./en/workspace.json";
import enProjects from "./en/projects.json";
import enAutopilots from "./en/autopilots.json";
import enAutomations from "./en/automations.json";
import enSkills from "./en/skills.json";
import enChat from "./en/chat.json";
import enModals from "./en/modals.json";
import enRuntimes from "./en/runtimes.json";
import enLayout from "./en/layout.json";
import enUsage from "./en/usage.json";
import enUi from "./en/ui.json";
import enSquads from "./en/squads.json";
import enBilling from "./en/billing.json";
import zhHansCommon from "./zh-Hans/common.json";
import zhHansAuth from "./zh-Hans/auth.json";
import zhHansSettings from "./zh-Hans/settings.json";
import zhHansIssues from "./zh-Hans/issues.json";
import zhHansAgents from "./zh-Hans/agents.json";
import zhHansEditor from "./zh-Hans/editor.json";
import zhHansOnboarding from "./zh-Hans/onboarding.json";
import zhHansInvite from "./zh-Hans/invite.json";
import zhHansLabels from "./zh-Hans/labels.json";
import zhHansMembers from "./zh-Hans/members.json";
import zhHansMyIssues from "./zh-Hans/my-issues.json";
import zhHansSearch from "./zh-Hans/search.json";
import zhHansInbox from "./zh-Hans/inbox.json";
import zhHansWorkspace from "./zh-Hans/workspace.json";
import zhHansProjects from "./zh-Hans/projects.json";
import zhHansAutopilots from "./zh-Hans/autopilots.json";
import zhHansAutomations from "./zh-Hans/automations.json";
import zhHansSkills from "./zh-Hans/skills.json";
import zhHansChat from "./zh-Hans/chat.json";
import zhHansModals from "./zh-Hans/modals.json";
import zhHansRuntimes from "./zh-Hans/runtimes.json";
import zhHansLayout from "./zh-Hans/layout.json";
import zhHansUsage from "./zh-Hans/usage.json";
import zhHansUi from "./zh-Hans/ui.json";
import zhHansSquads from "./zh-Hans/squads.json";
import zhHansBilling from "./zh-Hans/billing.json";
import uzCommon from "./uz/common.json";
import uzAuth from "./uz/auth.json";
import uzSettings from "./uz/settings.json";
import uzIssues from "./uz/issues.json";
import uzAgents from "./uz/agents.json";
import uzEditor from "./uz/editor.json";
import uzOnboarding from "./uz/onboarding.json";
import uzInvite from "./uz/invite.json";
import uzLabels from "./uz/labels.json";
import uzMembers from "./uz/members.json";
import uzMyIssues from "./uz/my-issues.json";
import uzSearch from "./uz/search.json";
import uzInbox from "./uz/inbox.json";
import uzWorkspace from "./uz/workspace.json";
import uzProjects from "./uz/projects.json";
import uzAutopilots from "./uz/autopilots.json";
import uzAutomations from "./uz/automations.json";
import uzSkills from "./uz/skills.json";
import uzChat from "./uz/chat.json";
import uzModals from "./uz/modals.json";
import uzRuntimes from "./uz/runtimes.json";
import uzLayout from "./uz/layout.json";
import uzUsage from "./uz/usage.json";
import uzUi from "./uz/ui.json";
import uzSquads from "./uz/squads.json";
import uzBilling from "./uz/billing.json";
import ruCommon from "./ru/common.json";
import ruAuth from "./ru/auth.json";
import ruSettings from "./ru/settings.json";
import ruIssues from "./ru/issues.json";
import ruAgents from "./ru/agents.json";
import ruEditor from "./ru/editor.json";
import ruOnboarding from "./ru/onboarding.json";
import ruInvite from "./ru/invite.json";
import ruLabels from "./ru/labels.json";
import ruMembers from "./ru/members.json";
import ruMyIssues from "./ru/my-issues.json";
import ruSearch from "./ru/search.json";
import ruInbox from "./ru/inbox.json";
import ruWorkspace from "./ru/workspace.json";
import ruProjects from "./ru/projects.json";
import ruAutopilots from "./ru/autopilots.json";
import ruAutomations from "./ru/automations.json";
import ruSkills from "./ru/skills.json";
import ruChat from "./ru/chat.json";
import ruModals from "./ru/modals.json";
import ruRuntimes from "./ru/runtimes.json";
import ruLayout from "./ru/layout.json";
import ruUsage from "./ru/usage.json";
import ruUi from "./ru/ui.json";
import ruSquads from "./ru/squads.json";
import ruBilling from "./ru/billing.json";

// Single source of truth for the resource bundle. Both apps (web layout +
// desktop App.tsx) import from here so adding a locale or namespace happens
// in exactly one place.
export const RESOURCES: Record<SupportedLocale, LocaleResources> = {
  en: {
    common: enCommon,
    auth: enAuth,
    settings: enSettings,
    issues: enIssues,
    agents: enAgents,
    editor: enEditor,
    onboarding: enOnboarding,
    invite: enInvite,
    labels: enLabels,
    members: enMembers,
    "my-issues": enMyIssues,
    search: enSearch,
    inbox: enInbox,
    workspace: enWorkspace,
    projects: enProjects,
    autopilots: enAutopilots,
    automations: enAutomations,
    skills: enSkills,
    chat: enChat,
    modals: enModals,
    runtimes: enRuntimes,
    layout: enLayout,
    usage: enUsage,
    ui: enUi,
    squads: enSquads,
    billing: enBilling,
  },
  "zh-Hans": {
    common: zhHansCommon,
    auth: zhHansAuth,
    settings: zhHansSettings,
    issues: zhHansIssues,
    agents: zhHansAgents,
    editor: zhHansEditor,
    onboarding: zhHansOnboarding,
    invite: zhHansInvite,
    labels: zhHansLabels,
    members: zhHansMembers,
    "my-issues": zhHansMyIssues,
    search: zhHansSearch,
    inbox: zhHansInbox,
    workspace: zhHansWorkspace,
    projects: zhHansProjects,
    autopilots: zhHansAutopilots,
    automations: zhHansAutomations,
    skills: zhHansSkills,
    chat: zhHansChat,
    modals: zhHansModals,
    runtimes: zhHansRuntimes,
    layout: zhHansLayout,
    usage: zhHansUsage,
    ui: zhHansUi,
    squads: zhHansSquads,
    billing: zhHansBilling,
  },
  uz: {
    common: uzCommon,
    auth: uzAuth,
    settings: uzSettings,
    issues: uzIssues,
    agents: uzAgents,
    editor: uzEditor,
    onboarding: uzOnboarding,
    invite: uzInvite,
    labels: uzLabels,
    members: uzMembers,
    "my-issues": uzMyIssues,
    search: uzSearch,
    inbox: uzInbox,
    workspace: uzWorkspace,
    projects: uzProjects,
    autopilots: uzAutopilots,
    automations: uzAutomations,
    skills: uzSkills,
    chat: uzChat,
    modals: uzModals,
    runtimes: uzRuntimes,
    layout: uzLayout,
    usage: uzUsage,
    ui: uzUi,
    squads: uzSquads,
    billing: uzBilling,
  },
  ru: {
    common: ruCommon,
    auth: ruAuth,
    settings: ruSettings,
    issues: ruIssues,
    agents: ruAgents,
    editor: ruEditor,
    onboarding: ruOnboarding,
    invite: ruInvite,
    labels: ruLabels,
    members: ruMembers,
    "my-issues": ruMyIssues,
    search: ruSearch,
    inbox: ruInbox,
    workspace: ruWorkspace,
    projects: ruProjects,
    autopilots: ruAutopilots,
    automations: ruAutomations,
    skills: ruSkills,
    chat: ruChat,
    modals: ruModals,
    runtimes: ruRuntimes,
    layout: ruLayout,
    usage: ruUsage,
    ui: ruUi,
    squads: ruSquads,
    billing: ruBilling,
  },
};
