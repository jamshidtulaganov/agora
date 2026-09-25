// Knowledge-base citations in Assistant replies.
//
// The Assistant cites a section inline as `[kb:xxxxxxxx]` (8 lowercase hex
// digits of the chunk id; the plan's `[[kb:…]]` double-bracket form is read
// too). The same conversation's `search_knowledge` / `read_knowledge` tool
// results carry the sections it saw, each with that `cite`. A token is shown as
// a source chip ONLY when one of those results has it — a cite the model made
// up, or one from a result this build can't read, is dropped, never shown as a
// raw id (docs/workspace-knowledge-plan.md §7).

import type { AssistantMessage } from "@agora/core/types";

export interface KnowledgeCite {
  cite: string;
  docId: string;
  docTitle: string;
  headingPath: string;
  location: string;
  /** The section's ord in the document, for the viewer deep link. */
  section: number | null;
}

const KNOWLEDGE_TOOL_NAMES = new Set(["search_knowledge", "read_knowledge"]);

const CITE_PATTERN = /^kb:[0-9a-f]{8}$/;

/** Tool results are arbitrary JSON; don't walk a pathological one forever. */
const MAX_DEPTH = 6;

function readCite(value: Record<string, unknown>): KnowledgeCite | null {
  const cite = typeof value.cite === "string" ? value.cite.trim().toLowerCase() : "";
  const docId = typeof value.doc_id === "string" ? value.doc_id : "";
  if (!CITE_PATTERN.test(cite) || !docId) return null;
  const section =
    typeof value.section === "number" && Number.isInteger(value.section) && value.section >= 0
      ? value.section
      : null;
  const text = (key: string) => (typeof value[key] === "string" ? (value[key] as string).trim() : "");
  return {
    cite,
    docId,
    docTitle: text("doc_title"),
    headingPath: text("heading_path"),
    location: text("location"),
    section,
  };
}

function collect(value: unknown, depth: number, into: Map<string, KnowledgeCite>): void {
  if (depth > MAX_DEPTH || value === null || typeof value !== "object") return;
  if (Array.isArray(value)) {
    for (const item of value) collect(item, depth + 1, into);
    return;
  }
  const record = value as Record<string, unknown>;
  const found = readCite(record);
  // First sighting wins: a later read_knowledge window repeats the same
  // sections, and the first result is the one the model answered from.
  if (found && !into.has(found.cite)) into.set(found.cite, found);
  for (const child of Object.values(record)) {
    if (child !== null && typeof child === "object") collect(child, depth + 1, into);
  }
}

/** Every citable section the conversation's knowledge tool calls returned. */
export function collectKnowledgeCites(messages: AssistantMessage[]): Map<string, KnowledgeCite> {
  const cites = new Map<string, KnowledgeCite>();
  for (const message of messages) {
    if (message.role !== "tool" || !KNOWLEDGE_TOOL_NAMES.has(message.tool_name ?? "")) continue;
    collect(message.tool_result, 0, cites);
  }
  return cites;
}

/** "Collections SOP · p. 4" — the chip's text. */
export function knowledgeCiteLabel(cite: KnowledgeCite): string {
  return [cite.docTitle || cite.headingPath, cite.location].filter(Boolean).join(" · ");
}

// `[kb:1a2b3c4d]`, `[[kb:1a2b3c4d]]`, or several in one bracket separated by
// commas / semicolons (`[kb:1a2b3c4d, kb:5e6f7a8b]`). The optional leading
// horizontal whitespace is captured so a dropped token doesn't leave a stray
// space before the punctuation that followed it.
const TOKEN_PATTERN =
  /([ \t]*)\[\[?\s*(kb:[0-9a-fA-F]{8}(?:\s*[,;]\s*kb:[0-9a-fA-F]{8})*)\s*\]\]?/g;

/**
 * Rewrites the citation tokens in `content`. Matched cites become
 * `render(cite)` (joined with a space when a bracket held several); tokens with
 * no match are removed. Content without tokens comes back unchanged.
 */
export function replaceKnowledgeCites(
  content: string,
  cites: Map<string, KnowledgeCite>,
  render: (cite: KnowledgeCite) => string,
): string {
  if (!content.includes("kb:")) return content;
  return content.replace(TOKEN_PATTERN, (_match, leading: string, group: string) => {
    const rendered = group
      .split(/[,;]/)
      .map((part) => cites.get(part.trim().toLowerCase()))
      .filter((cite): cite is KnowledgeCite => cite !== undefined)
      .map(render);
    return rendered.length > 0 ? `${leading || ""}${rendered.join(" ")}` : "";
  });
}

/** Scheme + type of the markdown link a matched cite becomes. */
export const KNOWLEDGE_CITE_MENTION_TYPE = "kb";

/** Markdown the message renderer draws as a chip via its mention renderer. */
export function knowledgeCiteMarkdown(cite: KnowledgeCite): string {
  // The link text is only a fallback (the renderer draws the chip from the
  // cite map); keep it free of characters that would end the link early.
  const text = knowledgeCiteLabel(cite).replace(/[[\]\\]/g, " ").trim() || cite.cite;
  return `[${text}](mention://${KNOWLEDGE_CITE_MENTION_TYPE}/${cite.cite.slice(3)})`;
}

/** Plain-text form, for Copy: "(Collections SOP · p. 4)". */
export function knowledgeCitePlainText(cite: KnowledgeCite): string {
  const label = knowledgeCiteLabel(cite);
  return label ? `(${label})` : "";
}

/**
 * The sources to list under a reply that used the knowledge base but cited
 * nothing inline: up to `max` documents (first section each, in the order
 * the search ranked them) from the knowledge tool results of this turn —
 * the messages since the person's last message. Empty unless `index` is the
 * turn's final assistant text, and empty when the reply already cites a
 * known source inline. Models don't always follow the cite rule; the answer
 * still shows where it came from.
 */
export function turnKnowledgeSources(
  messages: AssistantMessage[],
  index: number,
  cites: Map<string, KnowledgeCite>,
  max = 3,
): KnowledgeCite[] {
  const message = messages[index];
  if (!message || message.role !== "assistant" || !message.content.trim()) return [];
  const next = messages[index + 1];
  if (next && next.role !== "user") return [];
  if (hasKnownCite(message.content, cites)) return [];

  const turn: AssistantMessage[] = [];
  for (let i = index - 1; i >= 0; i--) {
    const m = messages[i];
    if (!m || m.role === "user") break;
    turn.unshift(m);
  }
  const sources: KnowledgeCite[] = [];
  const seenDocs = new Set<string>();
  for (const cite of collectKnowledgeCites(turn).values()) {
    if (seenDocs.has(cite.docId)) continue;
    seenDocs.add(cite.docId);
    sources.push(cite);
    if (sources.length >= max) break;
  }
  return sources;
}

function hasKnownCite(content: string, cites: Map<string, KnowledgeCite>): boolean {
  if (!content.includes("kb:")) return false;
  for (const match of content.matchAll(/kb:[0-9a-fA-F]{8}/g)) {
    if (cites.has(match[0].toLowerCase())) return true;
  }
  return false;
}
