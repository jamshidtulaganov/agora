"use client";

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { ChevronDown } from "lucide-react";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";

// Collapsed height cap for a long issue description. Beyond this the body is
// clamped behind a fade with a "Show more" toggle. ~320px ≈ 12 lines of body
// text — enough to read the lede of most descriptions without scrolling the
// whole comment thread out of reach.
const CLAMP_MAX_PX = 320;
// Hysteresis: only offer the toggle when the content is meaningfully taller
// than the cap. A description that overflows by a handful of pixels would
// clamp away almost nothing while still costing a "Show more" row — net
// noise. Require at least this much hidden content before clamping kicks in.
const CLAMP_SLACK_PX = 48;

interface CollapsibleDescriptionProps {
  /**
   * The description body. Measured via a ResizeObserver so markdown that
   * grows after mount (image decode, code highlight, table layout, KaTeX,
   * mermaid) re-evaluates the clamp instead of locking in a stale height.
   */
  children: ReactNode;
  /**
   * True while the description editor has focus. A clamped editor must never
   * hide the caret, so we force the body fully open whenever the user is
   * actively editing — the toggle reappears once focus leaves.
   */
  editing?: boolean;
  className?: string;
}

/**
 * Measured-height clamp for the issue description. Short descriptions render
 * unchanged (no toggle, no fade, no extra DOM cost beyond the wrapper). Once
 * the rendered body exceeds CLAMP_MAX_PX + CLAMP_SLACK_PX it collapses to
 * CLAMP_MAX_PX behind a bottom fade with a "Show more" / "Show less" toggle.
 *
 * Why measured height, not a line-clamp: the description is rich markdown
 * (tables, fenced code, images, math). `-webkit-line-clamp` truncates inline
 * text runs and corrupts block layout — a code block or table can't be
 * line-clamped. Capping `max-height` on the block container and fading the
 * overflow keeps every block intact and only hides what spills past the cap.
 *
 * Default collapsed when long, nothing persisted (matches the resolved-thread
 * / activity-block session-only model elsewhere in issue detail). The expand
 * transition respects `prefers-reduced-motion` via `motion-reduce`.
 */
export function CollapsibleDescription({
  children,
  editing = false,
  className,
}: CollapsibleDescriptionProps) {
  const { t } = useT("issues");
  const innerRef = useRef<HTMLDivElement>(null);
  // Whether the content is tall enough to warrant clamping. Until the first
  // measurement lands this is false, so a short description never flashes a
  // toggle and a long one never flashes fully-open before clamping.
  const [overflowing, setOverflowing] = useState(false);
  const [expanded, setExpanded] = useState(false);

  // Measure on mount and whenever the body resizes (async markdown). Driven by
  // ResizeObserver rather than a one-shot read so late layout (image decode,
  // lowlight, KaTeX) re-evaluates the clamp instead of locking a stale height.
  useEffect(() => {
    const el = innerRef.current;
    if (!el) return;
    const measure = () => {
      // scrollHeight is the full content height regardless of the cap applied
      // to the outer wrapper, so it's a stable basis for the overflow decision
      // even while collapsed.
      setOverflowing(el.scrollHeight > CLAMP_MAX_PX + CLAMP_SLACK_PX);
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const toggle = useCallback(() => setExpanded((v) => !v), []);

  // Editing forces the body open so the caret is never inside hidden content.
  // When not overflowing there's nothing to clamp. Otherwise honor the toggle.
  const showClamp = overflowing && !expanded && !editing;
  // The toggle row only appears when the content actually overflows; editing
  // keeps it hidden (the editor owns the surface while focused).
  const showToggle = overflowing && !editing;

  return (
    <div className={className}>
      <div
        className={cn(
          "relative overflow-hidden transition-[max-height] duration-300 ease-out motion-reduce:transition-none",
        )}
        style={{ maxHeight: showClamp ? CLAMP_MAX_PX : undefined }}
      >
        <div ref={innerRef}>{children}</div>
        {showClamp && (
          // Bottom fade — pointer-events-none so it never eats clicks meant
          // for the markdown beneath it (links, mentions, file cards). The
          // gradient rides `--background` so it's correct in light and dark.
          <div
            aria-hidden
            className="pointer-events-none absolute inset-x-0 bottom-0 h-16 bg-gradient-to-t from-background to-transparent"
          />
        )}
      </div>
      {showToggle && (
        <button
          type="button"
          onClick={toggle}
          aria-expanded={expanded}
          className="mt-1.5 flex items-center gap-1 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
        >
          <ChevronDown
            className={cn(
              "h-3.5 w-3.5 shrink-0 transition-transform motion-reduce:transition-none",
              expanded && "rotate-180",
            )}
          />
          <span>
            {expanded
              ? t(($) => $.detail.description_show_less)
              : t(($) => $.detail.description_show_more)}
          </span>
        </button>
      )}
    </div>
  );
}
