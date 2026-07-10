import type { Attachment } from "../types";

// The design-compare recipe (server/internal/handler/design_action.go,
// sliceActionDesignCompareContext) has the agent "screenshot both sides and
// attach them as evidence" on the same run_qa comment that carries the
// ```qa-result``` block — but the qa-result JSON schema has no field naming
// which attachment is which (see docs/design-stage-research.md §4). This
// module resolves the pair client-side, defensively, from the comment's
// attachment list — mirroring the filename-matching pattern
// DesignReviewDialog already uses for design-proposal screens
// (design-review-dialog.tsx:67-70), which DOES have a naming contract
// (`figma-<node-id>.png`, slice_action.go:212-213). We reuse that half of
// the convention (anything named `figma-*` is the reference render) and
// treat every other image on the comment as a "built" screenshot.

interface ScreenshotSourceComment {
  author_type: string;
  content: string;
  attachments?: Attachment[];
}

const QA_RESULT_FENCE_RE = /```qa-result\b/;

/**
 * Image attachments on the newest agent comment carrying a ```qa-result```
 * block — the comment the design-compare check posts its screenshots to.
 * Returns [] when no such comment exists (no run_qa yet, or run_qa attached
 * nothing). Mirrors `latestDesignAudit`'s "newest agent comment" convention.
 */
export function latestQAResultScreenshots(comments: ScreenshotSourceComment[]): Attachment[] {
  for (let i = comments.length - 1; i >= 0; i--) {
    const c = comments[i]!;
    if (c.author_type !== "agent") continue;
    if (!QA_RESULT_FENCE_RE.test(c.content ?? "")) continue;
    return (c.attachments ?? []).filter((a) => a.content_type.startsWith("image/"));
  }
  return [];
}

export interface DesignScreenshotPair {
  key: string;
  figma?: Attachment;
  built?: Attachment;
}

const FIGMA_FILENAME_RE = /^figma-/i;

/**
 * Pairs Figma-reference screenshots (`figma-<node-id>.png`) against "built"
 * screenshots (everything else) positionally. There is no server-side
 * contract pairing them explicitly, so this is a best-effort, order-
 * preserving match — an unequal count degrades to solo entries (rendered
 * alone by the caller) rather than dropping images.
 */
export function pairDesignScreenshots(images: Attachment[]): DesignScreenshotPair[] {
  const figmaImgs = images.filter((a) => FIGMA_FILENAME_RE.test(a.filename));
  const builtImgs = images.filter((a) => !FIGMA_FILENAME_RE.test(a.filename));
  const count = Math.max(figmaImgs.length, builtImgs.length);
  const pairs: DesignScreenshotPair[] = [];
  for (let i = 0; i < count; i++) {
    const figma = figmaImgs[i];
    const built = builtImgs[i];
    pairs.push({ key: figma?.id ?? built?.id ?? `pair-${i}`, figma, built });
  }
  return pairs;
}
