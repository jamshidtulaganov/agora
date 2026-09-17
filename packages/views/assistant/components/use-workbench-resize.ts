"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ASSISTANT_WORKBENCH_CHAT_MIN_W,
  ASSISTANT_WORKBENCH_PANE_MIN_W,
  useAssistantPanelStore,
} from "@agora/core/assistant";

/** How far one arrow key moves the divider. */
const KEYBOARD_STEP = 24;

function clamp(value: number, min: number, max: number) {
  return Math.max(min, Math.min(max, value));
}

export interface WorkbenchResize {
  /** Clamped width to render the pane at. */
  paneWidth: number;
  minWidth: number;
  /** Widest the pane may get before the transcript stops being readable. */
  maxWidth: number;
  isDragging: boolean;
  startDrag: (event: React.PointerEvent) => void;
  /** Move the divider by a keyboard step; positive widens the pane. */
  nudge: (delta: number) => void;
  /** Back to the default split. */
  reset: () => void;
}

/**
 * Divider between the transcript and the artifact pane.
 *
 * Same mechanics as the floating panel's `usePanelResize` — pointer capture,
 * document-level listeners, body cursor lock, a ResizeObserver for the bounds
 * — but one axis and a different bound: the panel is clamped to a share of its
 * container, while this is clamped so the column it is stealing from stays
 * usable. The stored width is the user's raw choice; the clamp happens at
 * render, so moving to a narrow window borrows width without overwriting the
 * preference the wide window will want back.
 */
export function useWorkbenchResize(
  containerRef: React.RefObject<HTMLElement | null>,
): WorkbenchResize {
  const storedWidth = useAssistantPanelStore((s) => s.workbenchPaneWidth);
  const setStoredWidth = useAssistantPanelStore((s) => s.setWorkbenchPaneWidth);
  const resetStoredWidth = useAssistantPanelStore((s) => s.resetWorkbenchPaneWidth);

  const [containerWidth, setContainerWidth] = useState(0);
  const [isDragging, setDragging] = useState(false);

  useEffect(() => {
    const element = containerRef.current;
    if (!element) return;

    // Only re-render when the measurement actually changed: a sub-pixel
    // ResizeObserver notification that feeds a setState back into the observer
    // is how "Maximum update depth exceeded" happens (see usePanelResize).
    const update = () =>
      setContainerWidth((previous) =>
        previous === element.clientWidth ? previous : element.clientWidth,
      );

    update();
    const observer = new ResizeObserver(update);
    observer.observe(element);
    return () => observer.disconnect();
  }, [containerRef]);

  // Before the first measurement (and under a test stub that never reports)
  // there is no upper bound to enforce — the minimum alone keeps it sane.
  const maxWidth =
    containerWidth > 0
      ? Math.max(ASSISTANT_WORKBENCH_PANE_MIN_W, containerWidth - ASSISTANT_WORKBENCH_CHAT_MIN_W)
      : Number.POSITIVE_INFINITY;
  const paneWidth = clamp(storedWidth, ASSISTANT_WORKBENCH_PANE_MIN_W, maxWidth);

  const boundsRef = useRef({ paneWidth, maxWidth });
  boundsRef.current = { paneWidth, maxWidth };

  const startDrag = useCallback(
    (event: React.PointerEvent) => {
      event.preventDefault();
      (event.target as HTMLElement).setPointerCapture?.(event.pointerId);

      const startX = event.clientX;
      const startWidth = boundsRef.current.paneWidth;
      setDragging(true);

      const onPointerMove = (moveEvent: PointerEvent) => {
        // The pane is pinned to the right edge: dragging left widens it.
        const raw = startWidth - (moveEvent.clientX - startX);
        setStoredWidth(clamp(raw, ASSISTANT_WORKBENCH_PANE_MIN_W, boundsRef.current.maxWidth));
      };

      const onPointerUp = () => {
        setDragging(false);
        document.removeEventListener("pointermove", onPointerMove);
        document.removeEventListener("pointerup", onPointerUp);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
      };

      document.addEventListener("pointermove", onPointerMove);
      document.addEventListener("pointerup", onPointerUp);
      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
    },
    [setStoredWidth],
  );

  const nudge = useCallback(
    (steps: number) => {
      const { paneWidth: current, maxWidth: max } = boundsRef.current;
      setStoredWidth(
        clamp(current + steps * KEYBOARD_STEP, ASSISTANT_WORKBENCH_PANE_MIN_W, max),
      );
    },
    [setStoredWidth],
  );

  return {
    paneWidth,
    minWidth: ASSISTANT_WORKBENCH_PANE_MIN_W,
    maxWidth,
    isDragging,
    startDrag,
    nudge,
    reset: resetStoredWidth,
  };
}
