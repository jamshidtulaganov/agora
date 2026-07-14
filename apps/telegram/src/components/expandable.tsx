import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useT } from "../i18n";
import { cn } from "../lib/cn";

// Collapses long content (agent replies, comments) behind a "Show more"
// toggle. Measures the rendered height, so it works for markdown too — the
// toggle only appears when the content actually overflows the cap.

export function ExpandableText({
  children,
  collapsedHeight = 176,
  className,
  fadeClass = "from-card",
}: {
  children: ReactNode;
  /** Max collapsed height in px (~9 lines of 13.5px chat text by default). */
  collapsedHeight?: number;
  className?: string;
  /** Matches the fade to the surface behind the text (default: card). */
  fadeClass?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [expanded, setExpanded] = useState(false);
  const [overflows, setOverflows] = useState(false);
  const t = useT();

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => setOverflows(el.scrollHeight > collapsedHeight + 12);
    measure();
    // Images/markdown load async — re-measure when the content box resizes.
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [collapsedHeight]);

  const collapsed = overflows && !expanded;

  return (
    <div className={className}>
      <div
        ref={ref}
        className={cn("relative overflow-hidden")}
        style={collapsed ? { maxHeight: collapsedHeight } : undefined}
      >
        {children}
        {collapsed && (
          <div
            className={cn(
              "pointer-events-none absolute inset-x-0 bottom-0 h-8 bg-gradient-to-t to-transparent",
              fadeClass,
            )}
          />
        )}
      </div>
      {overflows && (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="mt-1 text-[12.5px] font-semibold text-brand"
        >
          {expanded ? t("common.showLess") : t("common.showMore")}
        </button>
      )}
    </div>
  );
}
