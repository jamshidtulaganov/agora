// Pure helpers for rendering a `role: "tool"` transcript row as a compact
// action chip. `tool_result` is arbitrary, tool-specific JSON (see
// server/internal/assistant/tools.go's catalog) — these heuristics degrade
// gracefully on an unrecognized shape rather than throwing, per the API
// Response Compatibility rule (enum/shape drift downgrades, never crashes).

/** "search_issues" -> "search issues" — generic, works for any future tool
 *  name without needing a per-tool translation entry. */
export function humanizeToolName(name: string): string {
  return name.replace(/_/g, " ").trim();
}

/** Verbs a tool row can say it did — keys of `tool_chip.verb` in the locales. */
export const TOOL_VERBS = [
  "list", "search", "get", "create", "update", "delete", "add", "remove", "archive",
  "comment", "move", "invite", "attach", "mark", "resolve", "pin", "subscribe", "run",
  "set", "leave", "confirm", "dry_run", "propose", "check", "read",
] as const;
export type ToolVerb = (typeof TOOL_VERBS)[number];

/** Things a tool acts on — keys of `tool_chip.object` in the locales. */
export const TOOL_OBJECTS = [
  "workspaces", "workspace", "my_issues", "issues", "issue", "stale_issues", "comments",
  "comment", "projects", "project", "sprints", "sprint", "labels", "label", "issue_label",
  "issue_to_sprint", "agents", "agent", "squads", "members", "member", "member_role",
  "runtimes", "skills", "skill", "skill_to_agent", "autopilots", "autopilot", "autopilot_now",
  "automations", "automation", "automation_enabled", "integrations", "import_connections",
  "import", "import_mapping", "import_status", "inbox_read", "inbox", "item", "my_settings",
  "sidebar", "notification_preferences", "artifact", "plan", "usage", "activity", "qa_status",
  "zoho_account", "zoho_crm_modules", "zoho_crm_fields", "zoho_crm", "zoho_record",
  "zoho_departments", "zoho_tickets", "zoho_ticket", "zoho_conversation",
  "knowledge_base", "knowledge_document",
] as const;
export type ToolObject = (typeof TOOL_OBJECTS)[number];

/** Tools whose name has no leading verb: they read a status or a digest. */
const READ_ONLY_TOOLS: Record<string, ToolObject> = {
  usage_summary: "usage",
  activity_digest: "activity",
  inbox_summary: "inbox",
  qa_status: "qa_status",
  import_status: "import_status",
};

/** The workspace knowledge base: the tool names say "knowledge", the row
 *  says what was read ("Searched the knowledge base"). */
const KNOWLEDGE_TOOLS: Record<string, ToolDescription> = {
  search_knowledge: { verb: "search", object: "knowledge_base" },
  read_knowledge: { verb: "read", object: "knowledge_document" },
  list_knowledge: { verb: "list", object: "knowledge_base" },
};

/** The person's own Zoho, read-only: names that don't start with a verb. */
const ZOHO_TOOLS: Record<string, ToolDescription> = {
  zoho_whoami: { verb: "check", object: "zoho_account" },
  zoho_crm_modules: { verb: "list", object: "zoho_crm_modules" },
  zoho_crm_fields: { verb: "list", object: "zoho_crm_fields" },
  zoho_crm_search: { verb: "search", object: "zoho_crm" },
  zoho_crm_get_record: { verb: "get", object: "zoho_record" },
  zoho_desk_departments: { verb: "list", object: "zoho_departments" },
  zoho_desk_list_tickets: { verb: "list", object: "zoho_tickets" },
  zoho_desk_get_ticket: { verb: "get", object: "zoho_ticket" },
  zoho_desk_ticket_conversation: { verb: "get", object: "zoho_conversation" },
};

// Longest first, so "dry_run_import" is not read as verb "dry".
const VERBS_BY_LENGTH = [...TOOL_VERBS].sort((a, b) => b.length - a.length);

export interface ToolDescription {
  verb: ToolVerb;
  object: ToolObject;
}

/**
 * Split a tool name into a translatable verb + object ("list_my_issues" →
 * list + my_issues → "Looked up your issues"). Null for a name this build
 * does not know — a newer server's tool — so the row falls back to the
 * humanized raw name instead of a wrong sentence.
 */
export function describeTool(name: string): ToolDescription | null {
  const knowledge = KNOWLEDGE_TOOLS[name];
  if (knowledge) return knowledge;
  const zoho = ZOHO_TOOLS[name];
  if (zoho) return zoho;
  const readOnly = READ_ONLY_TOOLS[name];
  if (readOnly) return { verb: "check", object: readOnly };
  for (const verb of VERBS_BY_LENGTH) {
    if (verb === "check" || !name.startsWith(verb + "_")) continue;
    const object = name.slice(verb.length + 1);
    if ((TOOL_OBJECTS as readonly string[]).includes(object)) {
      return { verb, object: object as ToolObject };
    }
  }
  return null;
}

/** Fields checked, in priority order, for an obvious one-line summary. */
const SUMMARY_FIELDS = ["title", "name", "issue_key", "key", "id", "label", "url"] as const;

const MAX_SUMMARY_LENGTH = 140;

function truncate(value: string): string {
  return value.length > MAX_SUMMARY_LENGTH
    ? value.slice(0, MAX_SUMMARY_LENGTH).trimEnd() + "…"
    : value;
}

/** True when this tool_result is the server's `{"error": "..."}` shape. */
export function isToolResultError(result: unknown): result is { error: string } {
  return (
    !!result &&
    typeof result === "object" &&
    typeof (result as Record<string, unknown>).error === "string" &&
    (result as Record<string, unknown>).error !== ""
  );
}

/**
 * Best-effort one-line summary of a tool result for the action chip.
 * Returns null when nothing usable is found — the chip then shows just the
 * icon + humanized tool name.
 */
export function summarizeToolResult(result: unknown): string | null {
  if (result == null) return null;
  if (typeof result === "string") {
    return result.trim() ? truncate(result.trim()) : null;
  }
  if (typeof result !== "object") return String(result);

  const obj = result as Record<string, unknown>;

  if (isToolResultError(obj)) return truncate(obj.error);

  for (const field of SUMMARY_FIELDS) {
    const value = obj[field];
    if (typeof value === "string" && value.trim()) return truncate(value.trim());
  }

  const count = resultCount(obj);
  return count === null ? null : String(count);
}

/**
 * How many things a list-shaped result found. An exact server-side `total`
 * wins over the length of the (possibly capped) page it came with.
 */
export function resultCount(result: unknown): number | null {
  if (!result || typeof result !== "object" || Array.isArray(result)) return null;
  const obj = result as Record<string, unknown>;
  if (typeof obj.total === "number" && Number.isFinite(obj.total)) return obj.total;
  if (typeof obj.count === "number" && Number.isFinite(obj.count)) return obj.count;
  for (const value of Object.values(obj)) {
    if (Array.isArray(value)) return value.length;
  }
  return null;
}
