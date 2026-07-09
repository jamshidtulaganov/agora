"use client";

import { useCallback, useEffect, useState, type ReactNode } from "react";
import { useDefaultLayout, usePanelRef } from "react-resizable-panels";
import { ResizablePanelGroup, ResizablePanel, ResizableHandle } from "@agora/ui/components/ui/resizable";
import { Sheet, SheetContent } from "@agora/ui/components/ui/sheet";
import { useIsMobile } from "@agora/ui/hooks/use-mobile";

/**
 * Render-prop payload handed to a function `header`, so the caller can wire
 * its own rail-toggle affordance (e.g. a `PanelRight` button inside a
 * `BreadcrumbHeader`) to the frame's internal open/collapse state without
 * CockpitFrame needing to know what that control looks like.
 */
export interface CockpitRailToggle {
  open: boolean;
  toggle: () => void;
}

export interface CockpitFrameProps {
  /**
   * Header slot, rendered above `children` inside the content pane. Accepts
   * a plain node, or a function that receives `{ open, toggle }` for the
   * rail so the header can host its own toggle control.
   */
  header: ReactNode | ((rail: CockpitRailToggle) => ReactNode);
  /** Content pane body, rendered below `header` (and `topStrip`, if given). */
  children: ReactNode;
  /**
   * Right inspector pane content. The frame supplies the padding wrapper
   * (`p-4` on desktop, the Sheet's own `p-4` on mobile) — pass content only.
   */
  rail: ReactNode;
  /** Persistence key for the desktop panel-group layout. */
  layoutId: string;
  /** Whether the rail starts open on desktop. Default true. */
  defaultRailOpen?: boolean;
  /** Rail width constraints (desktop only). Defaults to 260 / 320 / 420. */
  railWidths?: { min?: number; default?: number; max?: number };
  /**
   * Reserved slot rendered between `header` and `children`, inside the
   * content pane. Renders nothing when omitted — reserved for the future
   * SDLC stepper strip (docs/sdlc-stage-cockpit-plan.md, phase C).
   */
  topStrip?: ReactNode;
}

const DEFAULT_RAIL_MIN = 260;
const DEFAULT_RAIL_WIDTH = 320;
const DEFAULT_RAIL_MAX = 420;

/**
 * Shared two-pane "detail cockpit" shell: a resizable content/rail split on
 * desktop, a `Sheet`-based rail on mobile. Extracted from issue-detail.tsx,
 * where this exact shell was copy-pasted across four detail pages (see
 * docs/sdlc-stage-cockpit-plan.md, section 1).
 *
 * The content pane owns `header` + `topStrip` + `children`, stacked in one
 * flex column — this matches the pre-extraction DOM, where the header lived
 * *inside* the resizable "content" panel rather than spanning the full
 * width above the rail. Preserving that nesting keeps issue-detail visually
 * and structurally identical after the refactor.
 */
export function CockpitFrame({
  header,
  children,
  rail,
  layoutId,
  defaultRailOpen = true,
  railWidths,
  topStrip,
}: CockpitFrameProps) {
  const { defaultLayout, onLayoutChanged } = useDefaultLayout({ id: layoutId });
  const railRef = usePanelRef();
  const isMobile = useIsMobile();
  const [desktopRailOpen, setDesktopRailOpen] = useState(defaultRailOpen);
  const [mobileRailOpen, setMobileRailOpen] = useState(false);

  useEffect(() => {
    if (isMobile) {
      setMobileRailOpen(false);
    }
  }, [isMobile]);

  const railOpen = isMobile ? mobileRailOpen : desktopRailOpen;

  const toggle = useCallback(() => {
    if (isMobile) {
      setMobileRailOpen((open) => !open);
      return;
    }
    const panel = railRef.current;
    if (!panel) return;
    if (panel.isCollapsed()) panel.expand();
    else panel.collapse();
  }, [isMobile, railRef]);

  const headerNode = typeof header === "function" ? header({ open: railOpen, toggle }) : header;

  const contentPane = (
    <div className="flex h-full min-w-0 flex-1 flex-col">
      {headerNode}
      {topStrip}
      {children}
    </div>
  );

  const min = railWidths?.min ?? DEFAULT_RAIL_MIN;
  const defaultWidth = railWidths?.default ?? DEFAULT_RAIL_WIDTH;
  const max = railWidths?.max ?? DEFAULT_RAIL_MAX;

  if (isMobile) {
    return (
      <div className="flex flex-1 min-h-0">
        {contentPane}
        <Sheet open={mobileRailOpen} onOpenChange={setMobileRailOpen}>
          <SheetContent side="right" showCloseButton={false} className="w-[320px] overflow-y-auto p-4">
            {rail}
          </SheetContent>
        </Sheet>
      </div>
    );
  }

  return (
    <ResizablePanelGroup orientation="horizontal" className="flex-1 min-h-0" defaultLayout={defaultLayout} onLayoutChanged={onLayoutChanged}>
      <ResizablePanel id="content" minSize="50%">
        {contentPane}
      </ResizablePanel>
      <ResizableHandle />
      <ResizablePanel
        id="sidebar"
        defaultSize={defaultRailOpen ? defaultWidth : 0}
        minSize={min}
        maxSize={max}
        collapsible
        groupResizeBehavior="preserve-pixel-size"
        panelRef={railRef}
        onResize={(size) => setDesktopRailOpen(size.inPixels > 0)}
      >
        <div className="overflow-y-auto border-l h-full">
          <div className="p-4">{rail}</div>
        </div>
      </ResizablePanel>
    </ResizablePanelGroup>
  );
}
