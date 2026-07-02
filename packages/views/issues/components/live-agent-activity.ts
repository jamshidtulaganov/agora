import type { TimelineItem } from "../../common/task-transcript";

// Maps a raw agent transcript item (Read / Edit / Write / Bash / Grep / …) to
// a short, human "what the agent is doing now" line for the live activity
// strip. Pure + dependency-free so it can be unit-tested and reused; the React
// strip only handles data plumbing and rendering.
//
// The returned shape is a verb key + an optional target, so the caller can
// localize the verb ("Reading" / "读取" / …) and keep the target (a file path
// or command) verbatim. Returning a pre-baked English string here would defeat
// i18n; returning structured parts lets the strip run the verb through `t()`.

// i18n key (under issues.json `live_activity.verb`) for each known tool's
// present-continuous verb. Anything not in this map falls back to the tool
// name itself as the verb (so a novel MCP tool still reads sensibly).
export type ActivityVerbKey =
  | "reading"
  | "editing"
  | "writing"
  | "searching"
  | "running"
  | "fetching"
  | "browsing"
  | "thinking"
  | "working";

export interface ActivityLine {
  verbKey: ActivityVerbKey | null;
  /**
   * Raw verb to show when `verbKey` is null — the tool's own name for an
   * unmapped tool (e.g. a custom MCP tool "deploy"). Already trimmed; never
   * localized.
   */
  rawVerb?: string;
  /** File path / command / query to show after the verb. May be empty. */
  target?: string;
}

// Tool-name → verb key. Tool names arrive exactly as the agent runtime emits
// them (Claude Code's PascalCase Read/Edit/Write/Bash/Grep/Glob/WebFetch/…).
// Matched case-insensitively so a runtime that lower-cases them still maps.
const TOOL_VERB: Record<string, ActivityVerbKey> = {
  read: "reading",
  edit: "editing",
  multiedit: "editing",
  notebookedit: "editing",
  write: "writing",
  create: "writing",
  grep: "searching",
  glob: "searching",
  search: "searching",
  bash: "running",
  shell: "running",
  webfetch: "fetching",
  fetch: "fetching",
  websearch: "searching",
  task: "working",
};

// Tools whose `input` carries a target path under one of these keys, in
// priority order. Mirrors the transcript dialog's getEventSummary extraction
// so the strip and the full transcript agree on what "the target" is.
const PATH_KEYS = ["file_path", "path", "notebook_path"] as const;

// Playwright MCP browser tools arrive as raw tool names — "mcp__playwright__
// browser_click", "mcp__plugin_playwright_playwright__browser_navigate",
// "mcp__MCP_DOCKER__browser_snapshot", etc. Without translation the QA live
// feed reads as a wall of "mcp__…__browser_snapshot" tool ids, so a reviewer
// can't tell what the browser is actually doing. Turn each into a short human
// action ("open /user/login", "click Войти", "fill form", "read console") that
// renders as a null-verbKey rawVerb + target. Returns null for non-browser
// tools so the normal TOOL_VERB path handles them. Pure + testable.
function pwStr(v: unknown): string {
  if (typeof v === "string" && v.trim()) return clamp(v.trim(), 50);
  if (typeof v === "number") return String(v);
  return "";
}

export function playwrightAction(
  toolName: string,
  input: Record<string, unknown> | undefined,
): { verb: string; target: string } | null {
  const lower = toolName.toLowerCase();
  if (!lower.startsWith("mcp__")) return null;
  const m = lower.match(/browser_([a-z_]+)$/);
  if (!m) return null;
  const action = m[1]!;
  const el =
    pwStr(input?.element) ||
    pwStr(input?.selector) ||
    pwStr(input?.ref) ||
    pwStr(input?.text);
  switch (action) {
    case "navigate": return { verb: "open", target: pwStr(input?.url) };
    case "navigate_back": return { verb: "go back", target: "" };
    case "click": return { verb: "click", target: el };
    case "type": return { verb: "type", target: pwStr(input?.text) || el };
    case "fill_form": return { verb: "fill", target: "form" };
    case "fill": return { verb: "fill", target: el };
    case "select_option": return { verb: "select", target: el };
    case "press_key": return { verb: "press", target: pwStr(input?.key) };
    case "hover": return { verb: "hover", target: el };
    case "drag": return { verb: "drag", target: el };
    case "snapshot": return { verb: "read page", target: "" };
    case "take_screenshot": return { verb: "screenshot", target: "" };
    case "console_messages": return { verb: "read console", target: pwStr(input?.level) };
    case "network_requests":
    case "network_request": return { verb: "check network", target: "" };
    case "wait_for": return { verb: "wait", target: pwStr(input?.text) || pwStr(input?.time) };
    case "evaluate":
    case "run_code":
    case "run_code_unsafe": return { verb: "run JS", target: "" };
    case "tabs": return { verb: "switch tab", target: "" };
    case "handle_dialog": return { verb: "dialog", target: "" };
    case "file_upload": return { verb: "upload", target: "" };
    default: return { verb: action.replace(/_/g, " "), target: el };
  }
}

/**
 * Collapse a long absolute path to its last two segments (".../dir/file.ts")
 * for compact display. Exported so the changes feed can shorten the path it
 * shows while keeping the full path around for a title attribute.
 */
export function shortenPath(p: string): string {
  const parts = p.split("/").filter(Boolean);
  if (parts.length <= 2) return parts.join("/") || p;
  return ".../" + parts.slice(-2).join("/");
}

function clamp(s: string, max: number): string {
  const trimmed = s.trim();
  return trimmed.length > max ? trimmed.slice(0, max - 1) + "…" : trimmed;
}

// Pull the most informative target string out of a tool_use input bag. Order
// matches the transcript dialog: explicit path → search pattern/query →
// command → description → first short string value.
function extractTarget(input: Record<string, unknown> | undefined): string {
  if (!input) return "";
  for (const key of PATH_KEYS) {
    const v = input[key];
    if (typeof v === "string" && v.trim()) return shortenPath(v.trim());
  }
  const pattern = input.pattern ?? input.query;
  if (typeof pattern === "string" && pattern.trim()) return clamp(pattern, 60);
  if (typeof input.command === "string" && input.command.trim()) {
    return clamp(input.command, 60);
  }
  if (typeof input.url === "string" && input.url.trim()) return clamp(input.url, 60);
  if (typeof input.description === "string" && input.description.trim()) {
    return clamp(input.description, 60);
  }
  for (const v of Object.values(input)) {
    if (typeof v === "string" && v.trim() && v.trim().length < 60) {
      return clamp(v, 60);
    }
  }
  return "";
}

/**
 * Reduce a built timeline to the single "current activity" line. Prefers the
 * most recent tool call (the concrete, glanceable action — "Editing routes.ts");
 * if the tail of the run is plain agent text or thinking with no trailing tool
 * call, falls back to a "thinking / working" line so the strip still reads as
 * alive. Returns null when there's nothing to show yet (empty transcript) — the
 * caller then shows its own neutral "working…" copy.
 */
export function deriveCurrentActivity(items: TimelineItem[]): ActivityLine | null {
  if (items.length === 0) return null;

  // Walk from the newest item back to the most recent tool_use. tool_result
  // entries are skipped — the action of interest is the call, not its output.
  for (let i = items.length - 1; i >= 0; i--) {
    const item = items[i]!;
    if (item.type === "tool_use") {
      const toolName = (item.tool ?? "").trim();
      const pw = playwrightAction(toolName, item.input);
      if (pw) return { verbKey: null, rawVerb: pw.verb, target: pw.target };
      const key = TOOL_VERB[toolName.toLowerCase()];
      return {
        verbKey: key ?? null,
        rawVerb: key ? undefined : toolName || undefined,
        target: extractTarget(item.input),
      };
    }
  }

  // No tool call yet (or the run is mid-thought): reflect the tail state.
  const last = items[items.length - 1]!;
  if (last.type === "thinking") return { verbKey: "thinking" };
  if (last.type === "text") return { verbKey: "working" };
  if (last.type === "error") return { verbKey: "working" };
  return { verbKey: "working" };
}

// ─────────────────────────────────────────────────────────────────────────────
// File-change feed
//
// A second, narrower lens on the same transcript: instead of "what is the agent
// doing right now" (any tool), this keeps ONLY the file MUTATIONS — the writes
// and edits — and turns each into a compact diff. Read / Bash / Grep / Glob /
// WebFetch and every other non-mutating tool are dropped entirely, so the feed
// is a live "git changes" view of the run rather than a tool-call log.
// ─────────────────────────────────────────────────────────────────────────────

/** A single file mutation distilled from one write/edit tool call. */
export interface FileChange {
  /** Stable per timeline item — used as the React list key. */
  key: string;
  /** Full path as the agent reported it (for a title attribute). */
  path: string;
  /** Last-two-segments display path (".../dir/file.ts"). */
  shortPath: string;
  /** "write" = whole-file create/overwrite; "edit" = in-place patch. */
  kind: "write" | "edit";
  additions: number;
  deletions: number;
  /** New content (Write) or new_string(s) (Edit) — the added side of the diff. */
  added: string;
  /** Replaced content — empty for Write, old_string(s) for Edit. */
  removed: string;
}

// Tool names (lower-cased) that mutate a file on disk. Everything else is
// excluded from the changes feed. Mirrors the write/edit half of TOOL_VERB.
const MUTATING_TOOLS = new Set([
  "write",
  "create",
  "edit",
  "multiedit",
  "notebookedit",
]);

function asString(v: unknown): string {
  return typeof v === "string" ? v : "";
}

// Count of lines in a chunk. An empty/whitespace-only string contributes 0 so a
// no-op side of an edit doesn't inflate the +/− badge. A non-empty chunk counts
// its newline-delimited lines (a trailing newline does not add a phantom line).
function countLines(s: string): number {
  if (!s) return 0;
  const trimmedTrailing = s.replace(/\n$/, "");
  if (trimmedTrailing.length === 0) return 0;
  return trimmedTrailing.split("\n").length;
}

// First present path key on a write/edit input bag (file_path → path →
// notebook_path), unshortened.
function rawPath(input: Record<string, unknown> | undefined): string {
  if (!input) return "";
  for (const key of PATH_KEYS) {
    const v = input[key];
    if (typeof v === "string" && v.trim()) return v.trim();
  }
  return "";
}

// Turn one mutating tool_use into a FileChange. `tool` is already lower-cased.
function toFileChange(
  key: string,
  tool: string,
  input: Record<string, unknown> | undefined,
): FileChange | null {
  const path = rawPath(input);
  if (!path) return null;
  const base = { key, path, shortPath: shortenPath(path) };

  if (tool === "write" || tool === "create") {
    const added = asString(input?.content);
    return {
      ...base,
      kind: "write",
      added,
      removed: "",
      additions: countLines(added),
      deletions: 0,
    };
  }

  if (tool === "multiedit") {
    const edits = Array.isArray(input?.edits) ? (input!.edits as unknown[]) : [];
    const addedParts: string[] = [];
    const removedParts: string[] = [];
    for (const e of edits) {
      if (e && typeof e === "object") {
        const rec = e as Record<string, unknown>;
        addedParts.push(asString(rec.new_string));
        removedParts.push(asString(rec.old_string));
      }
    }
    const added = addedParts.join("\n");
    const removed = removedParts.join("\n");
    return {
      ...base,
      kind: "edit",
      added,
      removed,
      additions: addedParts.reduce((n, s) => n + countLines(s), 0),
      deletions: removedParts.reduce((n, s) => n + countLines(s), 0),
    };
  }

  // edit + notebookedit: a single old→new swap. NotebookEdit carries the new
  // cell source under new_source (sometimes new_string) and, for an in-place
  // edit, may not report the prior source — so removed can be empty.
  const added = asString(input?.new_string) || asString(input?.new_source);
  const removed = asString(input?.old_string) || asString(input?.old_source);
  return {
    ...base,
    kind: "edit",
    added,
    removed,
    additions: countLines(added),
    deletions: countLines(removed),
  };
}

/**
 * Reduce a built timeline to the file MUTATIONS it contains, newest first.
 * Keeps only write/create/edit/multiedit/notebookedit tool calls (matched
 * case-insensitively); every other tool is excluded. Each kept call becomes a
 * {@link FileChange} carrying line counts and the added/removed diff sides.
 *
 * `key` is derived from the timeline index ("idx-N") rather than `seq`, so it
 * stays stable as live items append even though a TimelineItem may lack a seq.
 */
export function deriveFileChanges(items: TimelineItem[]): FileChange[] {
  const out: FileChange[] = [];
  for (let i = 0; i < items.length; i++) {
    const item = items[i]!;
    if (item.type !== "tool_use") continue;
    const tool = (item.tool ?? "").trim().toLowerCase();
    if (!MUTATING_TOOLS.has(tool)) continue;
    const change = toFileChange(`idx-${i}`, tool, item.input);
    if (change) out.push(change);
  }
  return out.reverse();
}

// ─────────────────────────────────────────────────────────────────────────────
// Step timeline
//
// A THIRD lens, for runs that touch no files — reviews, research, ops, anything
// that only reads/searches/runs commands. deriveFileChanges returns [] for those
// (nothing was written), which left the live panel blank ("working…" with no
// detail). deriveActivitySteps turns the SAME transcript into a readable
// step-by-step trail of what the agent did — "is reading wexRoutes.js", "is
// searching cron schedule", "is running agora issue comment". It is NOT a raw
// tool-call dump: each step is a localizable verb + a glanceable target, known
// CLI commands are summarized (no flags/ids/heredocs), and consecutive
// duplicates collapse. The panel shows this ONLY as the fallback when there are
// no file changes, so the git-style changes feed remains the view for coding.
// ─────────────────────────────────────────────────────────────────────────────

/** One step in the readable activity trail. */
export interface ActivityStep {
  /** Stable per timeline item — React list key. */
  key: string;
  /** Localizable verb ("reading" / "running" / …); null → use {@link rawVerb}. */
  verbKey: ActivityVerbKey | null;
  /** Verb to show verbatim when `verbKey` is null (an unmapped tool's name). */
  rawVerb?: string;
  /** File path / command summary / query shown after the verb. May be empty. */
  target?: string;
}

// Condense a shell command to a short, readable summary. Known platform/VCS
// commands collapse to a stable label (no ids, flags, or heredocs leaking into
// the UI); anything else is clamped verbatim. Returns command-summary DATA (like
// a file path), not translatable UI chrome, so it stays in this pure module.
function summarizeCommand(command: string): string {
  const c = command.trim();
  if (/\bagora\s+issue\s+comment\b/.test(c)) return "agora issue comment";
  const status = c.match(/\bagora\s+issue\s+status\s+\S+\s+["']?([a-zA-Z_]+)/);
  if (status) return `agora issue status → ${status[1]}`;
  const issueSub = c.match(/\bagora\s+issue\s+(\w+)/);
  if (issueSub) return `agora issue ${issueSub[1]}`;
  if (/\bgh\s+pr\s+create\b/.test(c) || /merge_request\.create/.test(c)) {
    return "gh pr create";
  }
  if (/\bgit\s+commit\b/.test(c)) return "git commit";
  if (/\bgit\s+push\b/.test(c)) return "git push";
  return clamp(c, 60);
}

function sameStep(a: ActivityStep, b: ActivityStep): boolean {
  return (
    a.verbKey === b.verbKey &&
    a.rawVerb === b.rawVerb &&
    (a.target ?? "") === (b.target ?? "")
  );
}

/**
 * Reduce a built timeline to a readable step trail, newest first. One step per
 * tool call: the verb comes from {@link TOOL_VERB} (bash/shell → "running" with
 * a summarized command), the target from {@link extractTarget} (or the command
 * summary for shells). Consecutive identical steps collapse to one. Like
 * deriveFileChanges, `key` is the timeline index so it stays stable as live
 * items append.
 */
export function deriveActivitySteps(items: TimelineItem[]): ActivityStep[] {
  const out: ActivityStep[] = [];
  for (let i = 0; i < items.length; i++) {
    const item = items[i]!;
    if (item.type !== "tool_use") continue;
    const toolName = (item.tool ?? "").trim();
    const tool = toolName.toLowerCase();

    let step: ActivityStep;
    const pw = playwrightAction(toolName, item.input);
    if (pw) {
      step = { key: `idx-${i}`, verbKey: null, rawVerb: pw.verb, target: pw.target };
    } else if (tool === "bash" || tool === "shell") {
      const command =
        typeof item.input?.command === "string" ? item.input.command : "";
      step = {
        key: `idx-${i}`,
        verbKey: "running",
        target: command ? summarizeCommand(command) : extractTarget(item.input),
      };
    } else {
      const key = TOOL_VERB[tool];
      step = {
        key: `idx-${i}`,
        verbKey: key ?? null,
        rawVerb: key ? undefined : toolName || undefined,
        target: extractTarget(item.input),
      };
    }

    const prev = out[out.length - 1];
    if (prev && sameStep(prev, step)) continue;
    out.push(step);
  }
  return out.reverse();
}
