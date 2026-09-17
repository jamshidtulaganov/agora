"use client";

import React from "react";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import type { WorkbenchResize } from "./use-workbench-resize";

/**
 * The grab strip between the transcript and the artifact pane.
 *
 * A 1px rule with a wider invisible hit area — the visible line only thickens
 * while it is being used (hover / focus / drag), so the split reads as a seam
 * rather than a control until you reach for it. Keyboard-operable as the ARIA
 * window-splitter pattern; double-click restores the default split.
 */
export function WorkbenchDivider({ resize }: { resize: WorkbenchResize }) {
  const { t } = useT("assistant");

  const handleKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "ArrowLeft") resize.nudge(1);
    else if (event.key === "ArrowRight") resize.nudge(-1);
    else if (event.key === "Home" || event.key === "Enter") resize.reset();
    else return;
    event.preventDefault();
  };

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={t(($) => $.workbench.resize)}
      aria-valuenow={Math.round(resize.paneWidth)}
      aria-valuemin={resize.minWidth}
      aria-valuemax={Number.isFinite(resize.maxWidth) ? Math.round(resize.maxWidth) : undefined}
      tabIndex={0}
      title={t(($) => $.workbench.resize_hint)}
      onPointerDown={resize.startDrag}
      onDoubleClick={resize.reset}
      onKeyDown={handleKeyDown}
      className="group relative z-10 -mr-1 w-2 shrink-0 cursor-col-resize touch-none focus-visible:outline-none"
    >
      <span
        aria-hidden
        className={cn(
          "pointer-events-none absolute inset-y-0 left-1/2 w-px -translate-x-1/2 bg-border transition-colors",
          "group-hover:bg-brand/60 group-focus-visible:bg-brand",
          resize.isDragging && "bg-brand",
        )}
      />
    </div>
  );
}
