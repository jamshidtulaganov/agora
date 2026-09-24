"use client";

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useProductTourStore } from "@agora/core/onboarding";
import { useCurrentWorkspace } from "@agora/core/paths";
import { Button } from "@agora/ui/components/ui/button";
import { useT } from "../i18n";

/** The stops, in order: each is a sidebar item carrying `data-nav-key`. */
const STOPS = ["workspace-switcher", "myIssues", "issues", "inbox", "assistant"] as const;
type Stop = (typeof STOPS)[number];
// Each nav stop's card is titled with the sidebar item's own label.
const NAV_LABEL = { myIssues: "my_issues", issues: "issues", inbox: "inbox", assistant: "assistant" } as const;

const CARD_WIDTH = 300;
const GAP = 12;
const PAD = 4;
// How long to wait for the sidebar to render its items after the setup
// navigates in, before giving up on a stop.
const FIND_TIMEOUT_MS = 2000;

function anchorFor(stop: Stop): HTMLElement | null {
  const el = document.querySelector<HTMLElement>(`[data-nav-key="${stop}"]`);
  if (!el) return null;
  const rect = el.getBoundingClientRect();
  return rect.width > 0 && rect.height > 0 ? el : null;
}

/**
 * The first-login "web tour": a spotlight that walks the sidebar — the
 * workspace switcher, My Issues, Issues, Inbox, the Assistant — with one
 * sentence each. Started by the member setup (useProductTourStore) and shown
 * only inside the workspace it opened. A stop whose item is not on screen
 * (hidden by the person, or a narrow window with the sidebar folded away) is
 * skipped; with nothing to point at, the tour quietly ends.
 */
export function ProductTour() {
  const tourWorkspaceId = useProductTourStore((s) => s.workspaceId);
  const workspace = useCurrentWorkspace();
  if (!tourWorkspaceId || workspace?.id !== tourWorkspaceId) return null;
  return <TourSpotlight />;
}

function TourSpotlight() {
  const { t } = useT("onboarding");
  const { t: tNav } = useT("layout");
  const stop = useProductTourStore((s) => s.stop);
  const [stops, setStops] = useState<Stop[] | null>(null);
  const [index, setIndex] = useState(0);
  const [rect, setRect] = useState<DOMRect | null>(null);
  const nextRef = useRef<HTMLButtonElement>(null);

  // Wait (briefly) for the sidebar, then keep the stops that are on screen.
  useEffect(() => {
    const started = Date.now();
    let frame = 0;
    const look = () => {
      const found = STOPS.filter((s) => anchorFor(s));
      if (found.length === STOPS.length || Date.now() - started > FIND_TIMEOUT_MS) {
        if (found.length === 0) stop();
        else setStops(found);
        return;
      }
      frame = window.requestAnimationFrame(look);
    };
    look();
    return () => window.cancelAnimationFrame(frame);
  }, [stop]);

  const current = stops?.[index] ?? null;

  const measure = useCallback(() => {
    if (!current) return;
    const el = anchorFor(current);
    setRect(el ? el.getBoundingClientRect() : null);
  }, [current]);

  useLayoutEffect(() => {
    measure();
    if (!current) return;
    const el = anchorFor(current);
    const observer = el ? new ResizeObserver(measure) : null;
    if (el) observer?.observe(el);
    window.addEventListener("resize", measure);
    window.addEventListener("scroll", measure, true);
    return () => {
      observer?.disconnect();
      window.removeEventListener("resize", measure);
      window.removeEventListener("scroll", measure, true);
    };
  }, [current, measure]);

  useEffect(() => {
    nextRef.current?.focus();
  }, [index, stops]);

  const isLast = !!stops && index === stops.length - 1;
  const goNext = useCallback(() => {
    if (isLast) stop();
    else setIndex((i) => i + 1);
  }, [isLast, stop]);
  const goBack = useCallback(() => setIndex((i) => Math.max(0, i - 1)), []);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        stop();
      } else if (event.key === "ArrowRight") {
        goNext();
      } else if (event.key === "ArrowLeft") {
        goBack();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [goNext, goBack, stop]);

  if (!stops || !current || !rect) return null;

  // The sidebar is on the left, so the card sits to the right of the item,
  // kept inside the viewport.
  const cardLeft = Math.min(rect.right + GAP, window.innerWidth - CARD_WIDTH - GAP);
  const cardTop = Math.max(GAP, Math.min(rect.top - 8, window.innerHeight - 220));
  const titleId = "product-tour-title";

  return createPortal(
    <div className="fixed inset-0 z-[60]" aria-live="polite">
      {/* The spotlight: a hole in a dim veil, drawn as one element's shadow,
          with an outline (not a ring, which is a shadow too) for the edge.
          The veil is darker in dark mode, where 35% black barely shows. */}
      <div
        aria-hidden="true"
        className="pointer-events-none fixed rounded-lg outline-2 outline-brand [--tour-veil:rgb(0_0_0/0.35)] motion-safe:transition-all motion-safe:duration-200 dark:[--tour-veil:rgb(0_0_0/0.6)]"
        style={{
          left: rect.left - PAD,
          top: rect.top - PAD,
          width: rect.width + PAD * 2,
          height: rect.height + PAD * 2,
          boxShadow: "0 0 0 9999px var(--tour-veil)",
        }}
      />
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className="fixed flex flex-col gap-3 rounded-xl border bg-popover p-4 text-popover-foreground shadow-lg"
        style={{ left: cardLeft, top: cardTop, width: CARD_WIDTH }}
      >
        <p className="text-xs text-muted-foreground">
          {t(($) => $.product_tour.counter, { step: index + 1, total: stops.length })}
        </p>
        <div className="flex flex-col gap-1">
          <h2 id={titleId} className="text-sm font-semibold">
            {current === "workspace-switcher"
              ? t(($) => $.product_tour.switcher_title)
              : tNav(($) => $.nav[NAV_LABEL[current]])}
          </h2>
          <p className="text-sm leading-relaxed text-muted-foreground">
            {t(($) => $.product_tour.stops[current].body)}
          </p>
        </div>
        <div className="flex items-center justify-between gap-2 pt-1">
          <Button variant="ghost" size="sm" className="-ml-2 text-muted-foreground" onClick={stop}>
            {t(($) => $.product_tour.skip)}
          </Button>
          <div className="flex items-center gap-1.5">
            {index > 0 && (
              <Button variant="outline" size="sm" onClick={goBack}>
                {t(($) => $.product_tour.back)}
              </Button>
            )}
            <Button ref={nextRef} size="sm" onClick={goNext}>
              {isLast ? t(($) => $.product_tour.done) : t(($) => $.product_tour.next)}
            </Button>
          </div>
        </div>
      </div>
    </div>,
    document.body,
  );
}
