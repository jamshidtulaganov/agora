// Slash-command catalog for the assistant composer.
//
// The composer stays a plain textarea — this is a menu, not an editor
// framework. Triggers live here (never translated: `/usage` is a command, not
// copy); labels, descriptions and the text a command inserts or sends are
// i18n keys resolved in slash-menu.tsx.

export type SlashCommandId = "add_task" | "my_tasks" | "find" | "usage" | "digest" | "project";

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
