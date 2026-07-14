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

// Shell tool names. A coding agent runs shell via many differently-named
// tools depending on runtime/MCP: Claude Code's "Bash", a generic "shell",
// and daemon/MCP variants like "exec_command" / "run_command" / "terminal".
// All carry the command under `input.command` and must go through the same
// summarize/classify path — otherwise (as happened for "exec_command") the
// generic branch dumps the raw `/bin/zsh -lc '…'` invocation into the UI.
const SHELL_TOOLS = new Set([
  "bash",
  "shell",
  "sh",
  "exec_command",
  "execute_command",
  "run_command",
  "run_terminal_cmd",
  "terminal",
  "command",
  "exec",
]);

function isShellTool(tool: string): boolean {
  return SHELL_TOOLS.has(tool);
}

// Unwrap a login-shell invocation to the command a human cares about:
//   /bin/zsh -lc 'git push -u origin …'  →  git push -u origin …
//   /bin/bash -c "npm test"              →  npm test
// The agent's shell tool wraps every command in `<shell> -lc '<cmd>'`; without
// unwrapping, classify/summarize see the wrapper (not the git/npm verb) and the
// UI shows the plumbing. Falls through to the original string when there's no
// recognizable wrapper. Exported for tests.
export function unwrapShell(command: string): string {
  const m = command
    .trim()
    .match(/^(?:\S*\/)?(?:ba|z)?sh\s+-[a-z]*c\s+(['"])([\s\S]*)\1\s*$/i);
  return m ? m[2]!.trim() : command.trim();
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
      const tool = toolName.toLowerCase();
      if (isShellTool(tool)) {
        const raw =
          typeof item.input?.command === "string" ? item.input.command : "";
        const command = raw ? unwrapShell(raw) : "";
        return {
          verbKey: "running",
          target: command ? summarizeCommand(command) : extractTarget(item.input),
        };
      }
      const key = TOOL_VERB[tool];
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
// Live file documents (the "spectator editor" view)
//
// A FOURTH lens: instead of a per-mutation diff feed, reconstruct — best-effort
// — the CURRENT text of every file the agent has touched this run, so the live
// editor can render it like a real code pane (line numbers, the fresh edit
// highlighted, the agent's cursor at the end). Reconstruction is stream-only:
//   Write        → the full file text is known exactly.
//   Edit         → applied in place when the old_string is found in the known
//                  text; otherwise the new_string is appended as a fragment
//                  after a "⋯" separator and the doc is marked partial.
//   MultiEdit    → each pair applied sequentially with the same rule.
// Partial docs are honest about being a keyhole view — the caller renders the
// separator lines distinctly and can label the doc as streamed fragments.
// ─────────────────────────────────────────────────────────────────────────────

/** A contiguous run of 0-based line indexes within {@link LiveFileDoc.text}. */
export interface FileDocRange {
  from: number;
  count: number;
}

/** Best-effort current text of one file the agent touched this run. */
export interface LiveFileDoc {
  /** Full path as the agent reported it. */
  path: string;
  /** Last-two-segments display path (".../dir/file.ts"). */
  shortPath: string;
  /** Reconstructed text (may be fragments joined by separator lines). */
  text: string;
  /** Line ranges written by this file's MOST RECENT mutation (the highlight). */
  ranges: FileDocRange[];
  /** Timeline index of the newest mutation — orders docs and keys reveals. */
  lastIdx: number;
  /** True when text is fragments (no full Write seen / an Edit didn't anchor). */
  partial: boolean;
}

/** Line used to separate non-contiguous fragments in a partial doc. */
export const FRAGMENT_SEPARATOR = "⋯";

// Lines spanned by a chunk when rendered (mid-line anchors still count their
// host line). Unlike countLines this never returns 0 for a non-empty chunk.
function spanLines(s: string): number {
  return s.split("\n").length;
}

function countNewlines(s: string): number {
  let n = 0;
  for (let i = 0; i < s.length; i++) if (s.charCodeAt(i) === 10) n++;
  return n;
}

/**
 * Reduce a built timeline to per-file reconstructed documents, newest-changed
 * first. Only write/create/edit/multiedit/notebookedit calls contribute. Pure
 * (no redaction — the renderer redacts per line like the diff feed does).
 */
export function deriveFileDocs(items: TimelineItem[]): LiveFileDoc[] {
  const docs = new Map<string, LiveFileDoc>();

  for (let i = 0; i < items.length; i++) {
    const item = items[i]!;
    if (item.type !== "tool_use") continue;
    const tool = (item.tool ?? "").trim().toLowerCase();
    if (!MUTATING_TOOLS.has(tool)) continue;
    const input = item.input;
    const path = rawPath(input);
    if (!path) continue;

    if (tool === "write" || tool === "create") {
      const content = asString(input?.content);
      docs.set(path, {
        path,
        shortPath: shortenPath(path),
        text: content,
        ranges: content ? [{ from: 0, count: spanLines(content) }] : [],
        lastIdx: i,
        partial: false,
      });
      continue;
    }

    // Edit-style tools: one or more old→new pairs, applied in order.
    const pairs: Array<{ oldStr: string; newStr: string }> = [];
    if (tool === "multiedit") {
      const edits = Array.isArray(input?.edits) ? (input!.edits as unknown[]) : [];
      for (const e of edits) {
        if (e && typeof e === "object") {
          const rec = e as Record<string, unknown>;
          pairs.push({
            oldStr: asString(rec.old_string),
            newStr: asString(rec.new_string),
          });
        }
      }
    } else {
      pairs.push({
        oldStr: asString(input?.old_string) || asString(input?.old_source),
        newStr: asString(input?.new_string) || asString(input?.new_source),
      });
    }
    if (pairs.length === 0) continue;

    const prev = docs.get(path);
    let text = prev?.text ?? "";
    let partial = prev?.partial ?? true;
    const ranges: FileDocRange[] = [];

    for (const { oldStr, newStr } of pairs) {
      if (!oldStr && !newStr) continue;
      const at = !partial && oldStr ? text.indexOf(oldStr) : -1;
      if (at >= 0) {
        // Anchored: replace in place; highlight the replacement's lines.
        text = text.slice(0, at) + newStr + text.slice(at + oldStr.length);
        if (newStr) {
          ranges.push({
            from: countNewlines(text.slice(0, at)),
            count: spanLines(newStr),
          });
        }
      } else if (newStr) {
        // Unanchored: append as a fragment after a separator line.
        const from = text ? spanLines(text) + 1 : 0;
        text = text ? `${text}\n${FRAGMENT_SEPARATOR}\n${newStr}` : newStr;
        ranges.push({ from, count: spanLines(newStr) });
        partial = true;
      }
      // Pure deletion on an unanchored doc: nothing renderable — skip.
    }

    docs.set(path, {
      path,
      shortPath: shortenPath(path),
      text,
      ranges,
      lastIdx: i,
      partial,
    });
  }

  return Array.from(docs.values()).sort((a, b) => b.lastIdx - a.lastIdx);
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
  /**
   * Human intent class for a shell command ("installing dependencies",
   * "running tests", …) — set when the command matches a known class so the
   * UI can show a localized phrase instead of raw shell. The raw summary stays
   * in {@link target} for hover/tooltips.
   */
  cmdClass?: CommandClass;
}

/** Recognized shell-command intents, localized under live_activity.cmd.*. */
export type CommandClass =
  | "install"
  | "test"
  | "lint"
  | "build"
  | "review"
  | "branch"
  | "commit"
  | "publish"
  | "pr"
  | "inspect";

// The classes a HUMAN reads as real progress (a milestone), not plumbing. The
// live step trail keeps only these — inspect/review/branch (git status/diff/
// checkout/…) are the agent's own bookkeeping and are dropped, so the human
// sees "Saving changes" / "Publishing the branch" / "Running the tests" instead
// of a wall of raw shell. See deriveMilestoneSteps.
const MILESTONE_CLASSES = new Set<CommandClass>([
  "install",
  "test",
  "lint",
  "build",
  "commit",
  "publish",
  "pr",
]);

// Order matters: first match wins.
// - STRONG inspect signals go first: a command that starts with a process/
//   sleep/word-count peek, or touches the agent's own tmp/task-output files,
//   is plumbing even when its arguments mention a test runner
//   (`ps aux | grep vitest` is a peek, not a test run).
// - Test before build (npm run test:e2e also matches the generic npm-run
//   shape).
// - WEAK inspect signals (ls/cat/tail/… at the start) come last so a piped
//   "npm test | tail" still reads as a test run.
const COMMAND_CLASS_RULES: Array<{ cls: CommandClass; re: RegExp }> = [
  { cls: "inspect", re: /^(ps|sleep|wc|du|stat|pwd|echo)\b|\/tmp\/claude|\/tasks\/[a-z0-9]+\.output/ },
  { cls: "install", re: /\b(npm ci|npm install|pnpm install|yarn install|composer install|pip install|go mod (download|tidy)|bundle install)\b/ },
  { cls: "test", re: /\b(vitest|jest|playwright|phpunit|pytest|codecept|go test)\b|\bnpm (run )?test\b|\btest:(e2e|unit|integration|smoke)\b|\bpnpm (run )?test\b/ },
  { cls: "lint", re: /\b(eslint|golangci-lint|prettier --check|lint:?\w*)\b|\bphp -l\b/ },
  { cls: "build", re: /\bnpm run build\b|\bpnpm (run )?build\b|\bgo build\b|\bvite build\b|\btsc\b|\bmake build\b|\bcomposer dump-autoload\b/ },
  // Milestone git verbs — a human wants to see these. Ordered before the
  // review/branch plumbing rules (git commit/push don't match those anyway,
  // but keep the intent grouped).
  { cls: "pr", re: /\bgh\s+pr\s+create\b|merge_request\.create/ },
  { cls: "commit", re: /\bgit\s+commit\b/ },
  { cls: "publish", re: /\bgit\s+push\b/ },
  { cls: "review", re: /\bgit (diff|log|show|status|blame)\b/ },
  { cls: "branch", re: /\bgit (checkout|switch|stash|reset|fetch|pull|rebase)\b/ },
  { cls: "inspect", re: /^(ls|cat|tail|head|grep|find)\b/ },
];

/**
 * Classify a shell command into a human intent class, or null when unknown.
 * Pure + exported for tests; the trail renderers map the class to a localized
 * phrase and fall back to the summarized command when null.
 */
export function classifyCommand(command: string): CommandClass | null {
  const c = command.trim();
  if (!c) return null;
  for (const rule of COMMAND_CLASS_RULES) {
    if (rule.re.test(c)) return rule.cls;
  }
  return null;
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
    } else if (isShellTool(tool)) {
      const raw =
        typeof item.input?.command === "string" ? item.input.command : "";
      // Unwrap `<shell> -lc '<cmd>'` so classify/summarize see the real verb.
      const command = raw ? unwrapShell(raw) : "";
      step = {
        key: `idx-${i}`,
        verbKey: "running",
        target: command ? summarizeCommand(command) : extractTarget(item.input),
      };
      const cls = command ? classifyCommand(command) : null;
      if (cls) step.cmdClass = cls;
    } else {
      const key = TOOL_VERB[tool];
      step = {
        key: `idx-${i}`,
        verbKey: key ?? null,
        rawVerb: key ? undefined : toolName || undefined,
        target: extractTarget(item.input),
      };
      // Reading its own background-task output is plumbing, not progress —
      // same "checking output" phrase as the equivalent shell peeks.
      if (key === "reading" && /\/tasks\/[a-z0-9]+\.output$|tmp\/claude/.test(step.target ?? "")) {
        step.cmdClass = "inspect";
      }
    }

    const prev = out[out.length - 1];
    if (prev && sameStep(prev, step)) continue;
    out.push(step);
  }
  return out.reverse();
}

/**
 * The human-facing step trail: {@link deriveActivitySteps} filtered to real
 * milestones. Drops the agent's plumbing — git status/diff/branch peeks, tmp
 * inspections, bare reads/searches — and keeps only the steps a non-engineer
 * reads as progress (dependency installs, test/build/lint runs, commits,
 * pushes, PR creation). File writes/edits are intentionally excluded too: they
 * are shown as their own diff feed (deriveFileChanges), so surfacing them here
 * would double-count. An empty result is honest ("working…") — better than a
 * wall of `git status --short`. Newest first (inherits deriveActivitySteps).
 */
export function deriveMilestoneSteps(items: TimelineItem[]): ActivityStep[] {
  return deriveActivitySteps(items).filter(
    (s) => s.cmdClass !== undefined && MILESTONE_CLASSES.has(s.cmdClass),
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Agent to-do list
//
// A coding agent maintains its own plan via the TodoWrite tool (Claude Code)
// or the runtime's normalized `todo_write` — each call REWRITES the whole list,
// so the latest such tool_use in the stream is the current plan. Surfacing it
// answers "what is the agent doing, and what's next" in the agent's OWN words,
// far more legible than any tool-call trail. The payload rides in the tool_use
// `input.todos` verbatim (daemon streams Tool+Input untouched).
// ─────────────────────────────────────────────────────────────────────────────

export type TodoStatus = "pending" | "in_progress" | "completed";

export interface TodoItem {
  /** The imperative task text ("Fix the greet button typo"). */
  content: string;
  status: TodoStatus;
  /**
   * Present-continuous form shown while in progress ("Fixing the greet button
   * typo"), when the agent provided one. Falls back to `content`.
   */
  activeForm?: string;
}

// The agent emits `PROGRESS: <sentence>` on each phase transition (runtime
// brief contract — see server/.../runtime_config.go "## Progress You Show The
// Human"). It rides in the agent's streamed text, so we scan text items for the
// latest such line. Anchored to the line start so a mention of the word
// "progress" mid-sentence never matches. Case-insensitive on the marker.
const PROGRESS_RE = /^\s*PROGRESS:\s*(.+?)\s*$/i;

/**
 * The agent's latest human-readable progress headline — the single "what's
 * happening now" sentence it authored, in its own (and the issue's) language.
 * This is the PRIMARY live signal: authored by the agent that knows its intent,
 * stage-agnostic, and never a reverse-engineered tool name. Returns null when
 * the agent hasn't emitted one (older runtime / agent that ignores the brief) —
 * callers fall back to the to-do's active item, then the derived milestone.
 */
export function deriveProgressHeadline(items: TimelineItem[]): string | null {
  let found: string | null = null;
  for (const item of items) {
    if (item.type !== "text" || !item.content) continue;
    for (const line of item.content.split("\n")) {
      const m = line.match(PROGRESS_RE);
      if (m) found = m[1]!;
    }
  }
  return found;
}

const TODO_TOOLS = new Set(["todowrite", "todo_write", "todo", "update_todos"]);

// Provider-agnostic plan fallback. Claude Code has a TodoWrite tool; codex /
// gemini / etc. do NOT, so a runtime without it would show no to-do list at
// all. The runtime brief therefore asks EVERY agent to also emit its checklist
// as a fenced ```todo block in its streamed text (see runtime_config.go). Each
// block REWRITES the whole list (like TodoWrite), so the latest block wins.
// Markers: `[ ]` pending, `[x]`/`[X]` done, `[~]`/`[>]` in progress.
const TODO_FENCE_RE = /```todo[^\n]*\n([\s\S]*?)```/gi;
const TODO_LINE_RE = /^\s*[-*]\s*\[([ xX~>])\]\s*(.+?)\s*$/;

function deriveTodosFromText(items: TimelineItem[]): TodoItem[] {
  let block: string | null = null;
  for (const item of items) {
    if (item.type !== "text" || !item.content) continue;
    // Latest fenced ```todo block anywhere in this text item wins.
    const re = new RegExp(TODO_FENCE_RE);
    let m: RegExpExecArray | null;
    while ((m = re.exec(item.content)) !== null) block = m[1]!;
  }
  if (block == null) return [];
  const out: TodoItem[] = [];
  for (const line of block.split("\n")) {
    const m = line.match(TODO_LINE_RE);
    if (!m) continue;
    const content = m[2]!.trim();
    if (!content) continue;
    const marker = m[1]!.toLowerCase();
    const status: TodoStatus =
      marker === "x" ? "completed" : marker === "~" || marker === ">" ? "in_progress" : "pending";
    out.push({ content, status });
  }
  return out;
}

/**
 * The agent's current to-do list. Prefers the most recent TodoWrite-style tool
 * call (Claude Code); falls back to the latest fenced ```todo block in the
 * agent's text (provider-agnostic — codex/gemini/etc.). Defensive: unknown
 * shapes / missing fields degrade to an empty list rather than throwing (the
 * payload is untyped JSON off the wire). Returns [] when the agent maintains no
 * plan. Preserves the agent's order.
 */
export function deriveTodos(items: TimelineItem[]): TodoItem[] {
  for (let i = items.length - 1; i >= 0; i--) {
    const item = items[i]!;
    if (item.type !== "tool_use") continue;
    if (!TODO_TOOLS.has((item.tool ?? "").trim().toLowerCase())) continue;
    const raw = (item.input as Record<string, unknown> | undefined)?.todos;
    // A TodoWrite call with a malformed payload → fall through to the text
    // block rather than showing nothing.
    if (!Array.isArray(raw)) return deriveTodosFromText(items);
    const out: TodoItem[] = [];
    for (const entry of raw) {
      if (!entry || typeof entry !== "object") continue;
      const e = entry as Record<string, unknown>;
      const content = typeof e.content === "string" ? e.content.trim() : "";
      if (!content) continue;
      const status: TodoStatus =
        e.status === "in_progress" || e.status === "completed"
          ? e.status
          : "pending";
      const activeForm =
        typeof e.activeForm === "string"
          ? e.activeForm.trim()
          : typeof e.active_form === "string"
            ? e.active_form.trim()
            : undefined;
      out.push(activeForm ? { content, status, activeForm } : { content, status });
    }
    return out;
  }
  // No TodoWrite-style tool call in the stream → try the fenced ```todo block.
  return deriveTodosFromText(items);
}
