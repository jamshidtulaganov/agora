"use client";

import React, { useRef, useCallback, useState, useEffect } from "react";
import {
  ASSISTANT_PANEL_MIN_W,
  ASSISTANT_PANEL_MIN_H,
  useAssistantPanelStore,
} from "@agora/core/assistant";
import type { PanelDragDir } from "./panel-resize-handles";

const MAX_RATIO = 0.9;
const FALLBACK_MAX_W = 800;
const FALLBACK_MAX_H = 700;

function clamp(v: number, min: number, max: number) {
  return Math.max(min, Math.min(max, v));
}

/**
 * Drag-to-resize + expand/restore for the floating assistant panel, bounded
 * by the panel's offset parent (the dashboard content area) rather than the
 * viewport, so the panel can never grow past the shell it lives in.
 */
export function usePanelResize(windowRef: React.RefObject<HTMLDivElement | null>) {
  const panelWidth = useAssistantPanelStore((s) => s.panelWidth);
  const panelHeight = useAssistantPanelStore((s) => s.panelHeight);
  const isExpanded = useAssistantPanelStore((s) => s.isExpanded);
  const setPanelSize = useAssistantPanelStore((s) => s.setPanelSize);
  const setExpanded = useAssistantPanelStore((s) => s.setExpanded);

  // ── Container bounds via ResizeObserver ────────────────────────────────
  const boundsRef = useRef({ maxW: FALLBACK_MAX_W, maxH: FALLBACK_MAX_H });
  const [boundsReady, setBoundsReady] = useState(false);
  const [isDragging, setIsDragging] = useState(false);
  const [, setRevision] = useState(0);

  useEffect(() => {
    const el = windowRef.current;
    const parent = el?.parentElement;
    if (!parent) return;

    const update = () => {
      const maxW = Math.floor(parent.clientWidth * MAX_RATIO);
      const maxH = Math.floor(parent.clientHeight * MAX_RATIO);
      setBoundsReady(true); // idempotent once true
      // Only re-render when the bounds actually changed. Without this guard a
      // spurious ResizeObserver notification (sub-pixel layout jitter during
      // mount) schedules a setState that feeds back into the observer,
      // producing "Maximum update depth exceeded".
      const prev = boundsRef.current;
      if (prev.maxW === maxW && prev.maxH === maxH) return;
      boundsRef.current = { maxW, maxH };
      setRevision((r) => r + 1);
    };

    update();

    const ro = new ResizeObserver(update);
    ro.observe(parent);
    return () => ro.disconnect();
  }, [windowRef]);

  // ── Derive rendered size ──────────────────────────────────────────────
  const { maxW, maxH } = boundsRef.current;

  const renderWidth = isExpanded
    ? maxW
    : clamp(panelWidth, ASSISTANT_PANEL_MIN_W, maxW);
  const renderHeight = isExpanded
    ? maxH
    : clamp(panelHeight, ASSISTANT_PANEL_MIN_H, maxH);

  // ── Expand / Restore ──────────────────────────────────────────────────
  const isAtMax = renderWidth >= maxW && renderHeight >= maxH;

  const toggleExpand = useCallback(() => {
    if (isExpanded || isAtMax) {
      setPanelSize(ASSISTANT_PANEL_MIN_W, ASSISTANT_PANEL_MIN_H);
    } else {
      setExpanded(true);
    }
  }, [isExpanded, isAtMax, setPanelSize, setExpanded]);

  // ── Drag ──────────────────────────────────────────────────────────────
  const dragRef = useRef<{
    startX: number;
    startY: number;
    startW: number;
    startH: number;
    dir: PanelDragDir;
  } | null>(null);

  const startDrag = useCallback(
    (e: React.PointerEvent, dir: PanelDragDir) => {
      e.preventDefault();
      (e.target as HTMLElement).setPointerCapture(e.pointerId);

      dragRef.current = {
        startX: e.clientX,
        startY: e.clientY,
        startW: renderWidth,
        startH: renderHeight,
        dir,
      };
      setIsDragging(true);

      const onPointerMove = (ev: PointerEvent) => {
        const d = dragRef.current;
        if (!d) return;

        const { maxW: mw, maxH: mh } = boundsRef.current;

        const rawW =
          dir === "left" || dir === "corner"
            ? d.startW - (ev.clientX - d.startX)
            : d.startW;
        const rawH =
          dir === "top" || dir === "corner"
            ? d.startH - (ev.clientY - d.startY)
            : d.startH;

        setPanelSize(
          clamp(rawW, ASSISTANT_PANEL_MIN_W, mw),
          clamp(rawH, ASSISTANT_PANEL_MIN_H, mh),
        );
      };

      const onPointerUp = () => {
        dragRef.current = null;
        setIsDragging(false);
        document.removeEventListener("pointermove", onPointerMove);
        document.removeEventListener("pointerup", onPointerUp);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
      };

      document.addEventListener("pointermove", onPointerMove);
      document.addEventListener("pointerup", onPointerUp);

      const cursorMap: Record<PanelDragDir, string> = {
        left: "col-resize",
        top: "row-resize",
        corner: "nw-resize",
      };
      document.body.style.cursor = cursorMap[dir];
      document.body.style.userSelect = "none";
    },
    [renderWidth, renderHeight, setPanelSize],
  );

  return {
    renderWidth,
    renderHeight,
    isAtMax,
    boundsReady,
    isDragging,
    toggleExpand,
    startDrag,
  };
}
