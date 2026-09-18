"use client";

import {
  Activity,
  BarChart3,
  Bot,
  CalendarRange,
  FolderPlus,
  Gauge,
  Inbox,
  ListTodo,
  Plus,
  Rocket,
  Search,
  ShieldCheck,
  Sunrise,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { SLASH_COMMANDS } from "../lib/slash-commands";
import type { SlashCommandAction, SlashCommandId } from "../lib/slash-commands";

export interface SlashCommandItem {
  id: SlashCommandId;
  trigger: string;
  action: SlashCommandAction;
  icon: LucideIcon;
  label: string;
  description: string;
  /** Template to prefill, or prompt to send — depending on `action`. */
  payload: string;
}

const ICONS: Record<SlashCommandId, LucideIcon> = {
  add_task: Plus,
  my_tasks: ListTodo,
  find: Search,
  usage: BarChart3,
  digest: Activity,
  project: FolderPlus,
  sprint_report: Gauge,
  standup: Sunrise,
  qa_health: ShieldCheck,
  release_notes: Rocket,
  plan_sprint: CalendarRange,
  triage_inbox: Inbox,
  new_agent: Bot,
};

/**
 * Resolves the command catalog against the active locale. A "send" command
 * whose request also exists as a launcher row reuses that row's prompt — the
 * same request should read identically whether the user clicked the row or
 * typed `/usage`. The management recipes have no row (the launcher stays at
 * 8), so they carry their own `prompt` key next to their label.
 */
export function useSlashCommands(): SlashCommandItem[] {
  const { t } = useT("assistant");

  const labels: Record<SlashCommandId, { label: string; description: string; payload: string }> = {
    add_task: {
      label: t(($) => $.composer.slash.add_task.label),
      description: t(($) => $.composer.slash.add_task.description),
      payload: t(($) => $.composer.slash.add_task.template),
    },
    my_tasks: {
      label: t(($) => $.composer.slash.my_tasks.label),
      description: t(($) => $.composer.slash.my_tasks.description),
      payload: t(($) => $.empty_state.prompts.my_plate),
    },
    find: {
      label: t(($) => $.composer.slash.find.label),
      description: t(($) => $.composer.slash.find.description),
      payload: t(($) => $.composer.slash.find.template),
    },
    usage: {
      label: t(($) => $.composer.slash.usage.label),
      description: t(($) => $.composer.slash.usage.description),
      payload: t(($) => $.empty_state.prompts.usage_this_week),
    },
    digest: {
      label: t(($) => $.composer.slash.digest.label),
      description: t(($) => $.composer.slash.digest.description),
      payload: t(($) => $.empty_state.prompts.today_digest),
    },
    project: {
      label: t(($) => $.composer.slash.project.label),
      description: t(($) => $.composer.slash.project.description),
      payload: t(($) => $.composer.slash.project.template),
    },
    sprint_report: {
      label: t(($) => $.composer.slash.sprint_report.label),
      description: t(($) => $.composer.slash.sprint_report.description),
      payload: t(($) => $.empty_state.prompts.sprint_report),
    },
    standup: {
      label: t(($) => $.composer.slash.standup.label),
      description: t(($) => $.composer.slash.standup.description),
      payload: t(($) => $.empty_state.prompts.standup),
    },
    qa_health: {
      label: t(($) => $.composer.slash.qa_health.label),
      description: t(($) => $.composer.slash.qa_health.description),
      payload: t(($) => $.empty_state.prompts.qa_health),
    },
    release_notes: {
      label: t(($) => $.composer.slash.release_notes.label),
      description: t(($) => $.composer.slash.release_notes.description),
      payload: t(($) => $.empty_state.prompts.release_notes),
    },
    plan_sprint: {
      label: t(($) => $.composer.slash.plan_sprint.label),
      description: t(($) => $.composer.slash.plan_sprint.description),
      payload: t(($) => $.composer.slash.plan_sprint.prompt),
    },
    triage_inbox: {
      label: t(($) => $.composer.slash.triage_inbox.label),
      description: t(($) => $.composer.slash.triage_inbox.description),
      payload: t(($) => $.composer.slash.triage_inbox.prompt),
    },
    new_agent: {
      label: t(($) => $.composer.slash.new_agent.label),
      description: t(($) => $.composer.slash.new_agent.description),
      payload: t(($) => $.composer.slash.new_agent.prompt),
    },
  };

  return SLASH_COMMANDS.map((command) => ({
    ...command,
    icon: ICONS[command.id],
    ...labels[command.id],
  }));
}

interface SlashMenuProps {
  commands: SlashCommandItem[];
  activeIndex: number;
  onHover: (index: number) => void;
  onPick: (command: SlashCommandItem) => void;
}

/**
 * The command list, anchored above the composer. Deliberately NOT the shadcn
 * `Command` primitive: cmdk owns its own input and keyboard handling, and the
 * whole point here is that focus never leaves the textarea — arrow keys and
 * Enter are handled by the composer's own keydown and merely reflected here.
 */
export function SlashMenu({ commands, activeIndex, onHover, onPick }: SlashMenuProps) {
  const { t } = useT("assistant");

  return (
    <div
      role="listbox"
      aria-label={t(($) => $.composer.slash.menu_label)}
      className="absolute bottom-full left-0 right-0 z-20 mb-2 overflow-hidden rounded-xl border border-border bg-popover p-1 shadow-md"
    >
      {commands.map((command, index) => (
        <button
          key={command.id}
          type="button"
          role="option"
          aria-selected={index === activeIndex}
          // Keep focus in the textarea: a blur would close the menu before
          // the click lands.
          onMouseDown={(e) => e.preventDefault()}
          onMouseEnter={() => onHover(index)}
          onClick={() => onPick(command)}
          className={cn(
            "flex w-full cursor-pointer items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-left transition-colors",
            index === activeIndex ? "bg-accent text-foreground" : "text-muted-foreground",
          )}
        >
          <command.icon className="size-3.5 shrink-0 opacity-70" />
          <span className="shrink-0 text-sm font-medium text-foreground">{command.label}</span>
          <span className="min-w-0 flex-1 truncate text-xs">{command.description}</span>
          <span className="shrink-0 font-mono text-xs opacity-70">{command.trigger}</span>
        </button>
      ))}
    </div>
  );
}
