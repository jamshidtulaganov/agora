// Slash-command catalog for the assistant composer.
//
// The composer stays a plain textarea — this is a menu, not an editor
// framework. Triggers live here (never translated: `/usage` is a command, not
// copy); labels, descriptions and the text a command inserts or sends are
// i18n keys resolved in slash-menu.tsx.

export type SlashCommandId =
  | "add_task"
  | "my_tasks"
  | "find"
  | "usage"
  | "digest"
  | "project"
  | "sprint_report"
  | "standup"
  | "qa_health"
  | "release_notes"
  | "plan_sprint"
  | "triage_inbox"
  | "new_agent";

/**
 * "template" prefills the composer and leaves the cursor at the end for the
 * user to finish the sentence; "send" fires a canned prompt immediately.
 */
export type SlashCommandAction = "template" | "send";

export interface SlashCommandDef {
  id: SlashCommandId;
  /** Typed trigger, leading slash included. */
  trigger: string;
  action: SlashCommandAction;
}

export const SLASH_COMMANDS: readonly SlashCommandDef[] = [
  { id: "add_task", trigger: "/add-task", action: "template" },
  { id: "my_tasks", trigger: "/my-tasks", action: "send" },
  { id: "find", trigger: "/find", action: "template" },
  { id: "usage", trigger: "/usage", action: "send" },
  { id: "digest", trigger: "/digest", action: "send" },
  { id: "project", trigger: "/project", action: "template" },
  // Domain recipes: one command -> one well-shaped report. The backend
  // prompt recognises these requests by phrase, so the payloads are the
  // launcher's own prompts rather than a second wording of the same ask.
  { id: "sprint_report", trigger: "/sprint-report", action: "send" },
  { id: "standup", trigger: "/standup", action: "send" },
  { id: "qa_health", trigger: "/qa-health", action: "send" },
  { id: "release_notes", trigger: "/release-notes", action: "send" },
  // Management recipes (docs/assistant-domain-plan.md §3b). Unlike the report
  // recipes above, these have no launcher row to borrow a prompt from — the
  // launcher deliberately stays at 8 rows — so their payload lives beside
  // their own label under `composer.slash.<id>.prompt`.
  { id: "plan_sprint", trigger: "/plan-sprint", action: "send" },
  { id: "triage_inbox", trigger: "/triage-inbox", action: "send" },
  { id: "new_agent", trigger: "/new-agent", action: "send" },
];

/**
 * Filter by what the user typed after the leading "/". Matches the trigger
 * from the start (so "/us" narrows to `/usage`) or the translated label
 * anywhere (so a Russian user can type the Russian word).
 */
export function filterSlashCommands<T extends { trigger: string; label: string }>(
  commands: readonly T[],
  query: string,
): T[] {
  const needle = query.trim().toLowerCase();
  if (!needle) return [...commands];
  return commands.filter(
    (command) =>
      command.trigger.slice(1).toLowerCase().startsWith(needle) ||
      command.label.toLowerCase().includes(needle),
  );
}

/**
 * True when this keystroke is the "/" that opens the menu: the first
 * character of an empty composer, and nothing else. Typing "/" mid-sentence
 * (a URL, a date, a fraction) must never pop a menu.
 */
export function opensSlashMenu(previousValue: string, nextValue: string): boolean {
  return previousValue === "" && nextValue === "/";
}
