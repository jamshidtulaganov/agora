"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import type { QADesignResult, Attachment } from "@agora/core/types";
import { issueTimelineOptions } from "@agora/core/issues/queries";
import { latestQAResultScreenshots, pairDesignScreenshots, type DesignScreenshotPair } from "@agora/core/design";
import { cn } from "@agora/ui/lib/utils";
import { useAttachmentPreview } from "../../editor";
import { useT } from "../../i18n";

// Renders the advisory design-verification result of a run_qa verdict: the
// pass/fail/skipped badge, the deterministic mismatch table (kind · selector
// · expected → actual), the design-system lint findings, and — when the
// design-compare check attached screenshots (design_action.go:
// sliceActionDesignCompareContext) — the Figma reference vs built screen,
// side by side (docs/design-stage-research.md §4, Phase 2). Reused inside
// the QA lens's checks section (compact) and the design lens's right column
// (full) — pass `issueId` to resolve screenshots, omit it to render the
// text-only recap (e.g. a caller with no timeline handy). Renders nothing
// when the issue carried no design result.
const VERDICT_STYLE: Record<string, string> = {
  pass: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
  fail: "bg-destructive/15 text-destructive",
  skipped: "bg-muted text-muted-foreground",
};

export function QADesignCompare({
  design,
  issueId,
  visual = "compact",
}: {
  design: QADesignResult | null | undefined;
  /** When provided, resolves the design-compare screenshots from the newest
   *  ```qa-result``` comment's attachments (see packages/core/design/screenshots.ts). */
  issueId?: string;
  /** "compact" = small thumbnails (QA lens's narrow review column). "full" =
   *  larger images (the design lens's right column, which has more room). */
  visual?: "compact" | "full";
}) {
  const { t } = useT("issues");

  const { data: timeline = [] } = useQuery({
    ...issueTimelineOptions(issueId ?? ""),
    enabled: !!issueId,
  });
  const screenshotPairs = useMemo(() => {
    if (!issueId) return [];
    const comments = timeline
      .filter((e) => e.type === "comment")
      .map((e) => ({ author_type: e.actor_type, content: e.content ?? "", attachments: e.attachments }));
    return pairDesignScreenshots(latestQAResultScreenshots(comments));
  }, [timeline, issueId]);

  if (!design) return null;

  const mismatchKindLabel = (kind: string): string => {
    switch (kind) {
      case "color":
        return t(($) => $.qa_review.mismatch_kind_color);
      case "typography":
        return t(($) => $.qa_review.mismatch_kind_typography);
      case "spacing":
        return t(($) => $.qa_review.mismatch_kind_spacing);
      case "layout":
        return t(($) => $.qa_review.mismatch_kind_layout);
      case "missing_element":
        return t(($) => $.qa_review.mismatch_kind_missing_element);
      default:
        return t(($) => $.qa_review.mismatch_kind_generic);
    }
  };

  const verdictLabel =
    design.verdict === "pass"
      ? t(($) => $.qa_review.design_pass)
      : design.verdict === "fail"
        ? t(($) => $.qa_review.design_fail)
        : t(($) => $.qa_review.design_skipped);

  return (
    <section>
      <div className="mb-1.5 flex items-center gap-2 text-[11px] uppercase tracking-wide text-muted-foreground">
        {t(($) => $.qa_review.design_heading)}
        <span className={`rounded px-1.5 py-0.5 text-[10px] font-medium normal-case ${VERDICT_STYLE[design.verdict] ?? VERDICT_STYLE.skipped}`}>
          {verdictLabel}
        </span>
        {design.reference_node && (
          <span className="font-mono text-[10px] normal-case text-muted-foreground/70">{design.reference_node}</span>
        )}
      </div>

      {/* Figma reference vs built screen — the human-reviewed secondary
          channel alongside the deterministic mismatch table below (the DOM
          assertions can miss composed-visual issues like overlap/z-order
          that a screenshot shows instantly). Renders nothing when no
          screenshots resolved (no issueId, or the qa-result comment attached
          none) — no separate empty state added here, per the surrounding
          section's own gating. */}
      {screenshotPairs.length > 0 && (
        <div className="mb-3">
          <DesignScreenshotGrid pairs={screenshotPairs} size={visual === "full" ? "lg" : "sm"} />
        </div>
      )}

      {/* Figma-compare status only when there's a real compare (reference node
          or mismatches); a lint-only result skips this so it doesn't read as
          "Figma unreachable". */}
      {design.verdict === "skipped" && (design.reference_node || (design.lint ?? []).length === 0) ? (
        <p className="rounded-lg border border-dashed bg-muted/20 px-3 py-2 text-[11px] text-muted-foreground">
          {t(($) => $.qa_review.design_skipped_reason)}
        </p>
      ) : design.verdict !== "skipped" && design.mismatches.length === 0 ? (
        <p className="rounded-lg border bg-muted/10 px-3 py-2 text-[11px] text-muted-foreground">
          {t(($) => $.qa_review.design_no_mismatches)}
        </p>
      ) : design.mismatches.length > 0 ? (
        <ul className="divide-y divide-border rounded-lg border">
          {design.mismatches.map((m, i) => (
            <li key={i} className="flex flex-col gap-0.5 px-3 py-1.5 text-[11px]">
              <div className="flex items-center gap-1.5">
                <span className="rounded bg-amber-500/15 px-1 py-0.5 text-[9px] font-medium uppercase text-amber-600 dark:text-amber-400">
                  {mismatchKindLabel(m.kind)}
                </span>
                {m.selector && <code className="truncate text-muted-foreground">{m.selector}</code>}
              </div>
              <div className="text-muted-foreground">
                <span className="text-emerald-600 dark:text-emerald-400">{m.expected}</span>
                {" → "}
                <span className="text-destructive">{m.actual}</span>
              </div>
            </li>
          ))}
        </ul>
      ) : null}

      {/* Diff-scoped design-system lint — what the change eroded. */}
      {design.lint && design.lint.length > 0 && (
        <div className="mt-2">
          <div className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t(($) => $.qa_review.design_lint_heading)}
          </div>
          <ul className="divide-y divide-border rounded-lg border">
            {design.lint.map((l, i) => (
              <li key={i} className="flex flex-col gap-0.5 px-3 py-1.5 text-[11px]">
                <div className="flex items-center gap-1.5">
                  <span
                    className={`rounded px-1 py-0.5 text-[9px] font-medium uppercase ${
                      l.severity === "block"
                        ? "bg-destructive/15 text-destructive"
                        : "bg-amber-500/15 text-amber-600 dark:text-amber-400"
                    }`}
                  >
                    {l.severity === "block" ? t(($) => $.qa_review.design_lint_block) : t(($) => $.qa_review.design_lint_warn)}
                  </span>
                  {l.where && <code className="truncate text-muted-foreground">{l.where}</code>}
                </div>
                {l.issue && <span className="text-muted-foreground">{l.issue}</span>}
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------------
// DesignScreenshotGrid — shared screenshot-pair renderer
// ---------------------------------------------------------------------------
//
// Exported so the design lens's primary visual pane (design-screenshot-
// compare.tsx) can mount the exact same pair rendering + lightbox at a
// larger size, instead of duplicating the click-to-preview wiring. Owns its
// own AttachmentPreviewModal instance (per CLAUDE.md: reuse the existing
// attachment/lightbox pattern, don't invent a new one) — safe to mount more
// than once on a page, each carries its own (collapsed) modal state.

export function DesignScreenshotGrid({
  pairs,
  size = "sm",
}: {
  pairs: DesignScreenshotPair[];
  size?: "sm" | "lg";
}) {
  const { t } = useT("issues");
  const preview = useAttachmentPreview();
  if (pairs.length === 0) return null;

  const heightClass = size === "lg" ? "h-64" : "h-24";

  return (
    <div className="space-y-3">
      {pairs.map((pair) => (
        <div key={pair.key} className={cn("grid gap-2", pair.figma && pair.built ? "grid-cols-2" : "grid-cols-1")}>
          {pair.figma && (
            <ScreenshotPane
              attachment={pair.figma}
              label={t(($) => $.qa_review.screenshot_figma)}
              heightClass={heightClass}
              onOpen={() => preview.tryOpen({ kind: "full", attachment: pair.figma! })}
            />
          )}
          {pair.built && (
            <ScreenshotPane
              attachment={pair.built}
              label={t(($) => $.qa_review.screenshot_built)}
              heightClass={heightClass}
              onOpen={() => preview.tryOpen({ kind: "full", attachment: pair.built! })}
            />
          )}
        </div>
      ))}
      {preview.modal}
    </div>
  );
}

function ScreenshotPane({
  attachment,
  label,
  heightClass,
  onOpen,
}: {
  attachment: Attachment;
  label: string;
  heightClass: string;
  onOpen: () => void;
}) {
  // Same URL fallback DesignReviewDialog already uses for this exact
  // category of image (Figma renders / screen captures on comment
  // attachments) — reused rather than re-deriving a CDN/signed-URL policy.
  const src = attachment.download_url || attachment.url;
  return (
    <figure className="space-y-1">
      <button
        type="button"
        onClick={onOpen}
        className={cn(
          "block w-full cursor-zoom-in overflow-hidden rounded-md border border-border bg-muted/10",
          heightClass,
        )}
      >
        <img src={src} alt={label} className="h-full w-full object-contain" />
      </button>
      <figcaption className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
        {label}
      </figcaption>
    </figure>
  );
}
