"use client";

/**
 * CollapsedFenceBlock — collapses long machine-payload code fences into a
 * one-line summary instead of dumping raw JSON into a comment thread.
 *
 * Agents append structured result blocks to comments (a run_test_cases
 * ```test-runs``` array, a compile_tests ```scripts``` listing, and similar
 * ```json```-tagged payloads) for the app to parse — the dedicated panels
 * (Test cases, QA evidence, etc.) read them from the raw comment content, so
 * a human reading the activity feed never needed the wall of JSON, only the
 * dedicated panel's summary of it. Short data blocks render normally (a
 * two-line JSON object isn't a "wall"); only fences over
 * COLLAPSE_LINE_THRESHOLD lines collapse, and only when they're actually
 * data-shaped (a known machine-result tag, or the body parses as JSON) — a
 * long human-pasted source-code snippet is untouched.
 *
 * Mounted by ReadonlyContent's `code` renderer. The `pre` renderer there
 * recognizes a to-be-collapsed fence by inspecting the unrendered `<code>`
 * element's className + text (same two-layer trick already used for
 * MermaidDiagram / HtmlBlockPreview) and unwraps it from the default `<pre>`
 * envelope — this component owns its own envelope (a button when collapsed,
 * CodeBlockStatic's own `<pre>` when expanded).
 */

import { useState, type ReactNode } from "react";
import { ChevronRight } from "lucide-react";
import { useT } from "../i18n";
import { CodeBlockStatic } from "./code-block-static";

/** Fences over this many lines are eligible to collapse. */
export const COLLAPSE_LINE_THRESHOLD = 8;

// Fence tags that carry machine-generated structured data — the "raw JSON
// walls" a human reading the thread never needs to read in full. Additions
// here MUST stay display-only (see stripAgentMachineBlocks in
// readonly-content.tsx for the sibling list of tags removed entirely).
const RAW_DATA_FENCE_TAGS = new Set([
  "json",
  "test-runs",
  "scripts",
  "qa-result",
  "deploy-result",
  "results",
]);

function countLines(code: string): number {
  return code.replace(/\n$/, "").split("\n").length;
}

function looksLikeJsonPayload(code: string): boolean {
  const trimmed = code.trim();
  if (!trimmed || !(trimmed.startsWith("{") || trimmed.startsWith("["))) return false;
  try {
    JSON.parse(trimmed);
    return true;
  } catch {
    return false;
  }
}

/** Shared collapse decision — used by both the `code` and `pre` renderers in
 * ReadonlyContent, so the two stay in agreement about which fence unwraps. */
export function shouldCollapseFence(lang: string | undefined, code: string): boolean {
  if (countLines(code) <= COLLAPSE_LINE_THRESHOLD) return false;
  if (lang && RAW_DATA_FENCE_TAGS.has(lang.toLowerCase())) return true;
  return looksLikeJsonPayload(code);
}

/** Flattens react-markdown's raw AST children (string | array | node) into
 * plain text, for the `pre` renderer's collapse pre-check — see its comment
 * on why it can't just check the resolved element type. */
export function fenceChildrenToText(children: ReactNode): string {
  if (typeof children === "string") return children;
  if (Array.isArray(children)) return children.map(fenceChildrenToText).join("");
  return "";
}

export function CollapsedFenceBlock({ lang, code }: { lang?: string; code: string }) {
  const { t } = useT("editor");
  const [expanded, setExpanded] = useState(false);
  const lineCount = countLines(code);
  const tag = lang || "json";

  if (!expanded) {
    return (
      <button
        type="button"
        onClick={() => setExpanded(true)}
        className="my-2 flex w-full items-center gap-1.5 rounded-md border bg-muted/30 px-2.5 py-1.5 text-left text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
      >
        <ChevronRight className="size-3 shrink-0" />
        <span className="min-w-0 flex-1 truncate font-mono">
          {t(($) => $.code_block.collapsed_lines, { count: lineCount, tag })}
        </span>
        <span className="ml-auto shrink-0 font-medium">{t(($) => $.code_block.expand)}</span>
      </button>
    );
  }

  return (
    <div className="my-2">
      <button
        type="button"
        onClick={() => setExpanded(false)}
        className="mb-1 flex items-center gap-1.5 text-xs text-muted-foreground transition-colors hover:text-foreground"
      >
        <ChevronRight className="size-3 shrink-0 rotate-90" />
        {t(($) => $.code_block.collapse)}
      </button>
      <CodeBlockStatic language={lang} body={code} />
    </div>
  );
}
