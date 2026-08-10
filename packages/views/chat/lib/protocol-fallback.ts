export type ProtocolTodoStatus = "done" | "active" | "pending";

export interface ProtocolTodoItem {
  status: ProtocolTodoStatus;
  text: string;
}

export interface ProtocolFallback {
  progress: string;
  todo: ProtocolTodoItem[];
  final: string;
  raw: string;
}

const progressPattern = /\bPROGRESS:\s*([\s\S]*?)(?=\s*```todo|\s*\bPROGRESS:|$)/gi;
const todoPattern = /```todo\s*([\s\S]*?)```/gi;

/**
 * Older/edge-degraded agent runs can persist their structured protocol as one
 * concatenated chat string. Parse only the unmistakable PROGRESS + todo-fence
 * shape so ordinary prose containing either word remains ordinary Markdown.
 */
export function parseProtocolFallback(content: string): ProtocolFallback | null {
  if (!/\bPROGRESS:/i.test(content) || !/```todo/i.test(content)) return null;

  let progress = "";
  for (const match of content.matchAll(progressPattern)) {
    const candidate = cleanSegment(match[1] ?? "");
    if (candidate) progress = candidate;
  }

  let latestTodo = "";
  let lastTodoEnd = -1;
  for (const match of content.matchAll(todoPattern)) {
    latestTodo = match[1] ?? "";
    lastTodoEnd = (match.index ?? 0) + match[0].length;
  }

  if (!progress || lastTodoEnd < 0) return null;

  const todo = parseTodoItems(latestTodo);
  const final = cleanSegment(content.slice(lastTodoEnd));
  return { progress, todo, final, raw: content };
}

function parseTodoItems(value: string): ProtocolTodoItem[] {
  // Some providers concatenate checklist rows onto one line. Restore the row
  // boundaries before parsing without changing text inside an item.
  const rows = value
    .replace(/\s+(?=-\s*\[[ xX~]\])/g, "\n")
    .split("\n");
  const items: ProtocolTodoItem[] = [];
  for (const row of rows) {
    const match = row.trim().match(/^-\s*\[([ xX~])\]\s*(.+)$/);
    if (!match) continue;
    const marker = match[1]?.toLowerCase();
    items.push({
      status: marker === "x" ? "done" : marker === "~" ? "active" : "pending",
      text: cleanSegment(match[2] ?? ""),
    });
  }
  return items;
}

function cleanSegment(value: string): string {
  return value
    .replace(/^request\./i, "")
    .replace(/^[.\s]+/, "")
    .replace(/\s+/g, " ")
    .trim();
}
