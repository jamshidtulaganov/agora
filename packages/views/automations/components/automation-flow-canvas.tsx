"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Filter, Maximize2, Minimize2, Minus, Plus, Scan, Workflow, Zap } from "lucide-react";
import { Button } from "@agora/ui/components/ui/button";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";

// The flow canvas — the interaction contract is borrowed from the two tools people
// already know (n8n: docs.n8n.io/build/keyboard-shortcuts; draw.io: fullscreen
// expands to the window, ⌘+wheel zooms, a plain wheel scrolls, the grid is always
// on and zooming never changes the diagram):
//
//   pan       background drag, middle-drag, space+drag, ⌘/ctrl+drag, wheel
//   zoom      ⌘/ctrl + wheel (around the cursor), +/-, buttons
//   0 reset · 1 fit · Enter opens the node · Delete removes the step
//   fullscreen toggle top-right (Esc exits), zoom cluster bottom-left
//
// The flow reads TOP → BOTTOM: a task automation is "when this, then these", and
// people read consequences downward — the vertical lane also survives narrow
// screens, where a horizontal chain forced immediate panning.
//
// Deliberate departures from the free-graph tools, because this flow is a LINEAR
// chain: dragging a node REORDERS it (order IS the model, so a node parked
// off-lane would be a lie about what runs next), and positions are derived, never
// stored, so a saved rule carries no coordinates that could drift from its steps.

const NODE_WIDTH = 280;
const NODE_HEIGHT = 76;
const NODE_GAP = 52;
const CANVAS_PAD = 40;
const MIN_ZOOM = 0.4;
const MAX_ZOOM = 2;
const ZOOM_STEP = 1.2;

export interface FlowCanvasNode {
  /** "trigger" for the first node, else the step index as a string. */
  id: string;
  kind: "trigger" | "filter" | "action";
  kicker: string;
  title: string;
  subtitle: string;
  /** The node's result in the LATEST run, when one exists and the draft is
   *  unedited: ok / failed / stopped (a filter ended the flow) / not_run
   *  (a step after the stop). Absent = no run to show. */
  outcome?: "ok" | "failed" | "stopped" | "not_run";
  /** Hover text for the outcome dot (the run status in the user's language). */
  outcomeLabel?: string;
}

interface AutomationFlowCanvasProps {
  nodes: FlowCanvasNode[];
  selectedId: string;
  onSelect: (id: string) => void;
  onOpen?: (id: string) => void;
  /** Insert a step at this index (0 = directly after the trigger). */
  onInsert?: (index: number) => void;
  /** Move the step at `from` to `to` (both step indexes, trigger excluded). */
  onReorder?: (from: number, to: number) => void;
  /** Remove the step at this index. */
  onRemove?: (index: number) => void;
  disabled?: boolean;
}

interface Viewport {
  x: number;
  y: number;
  zoom: number;
}

interface DragState {
  pointerId: number;
  stepIndex: number;
  startClientY: number;
  offsetY: number;
  dropIndex: number;
}

// Per-kind accents, all semantic tokens (no hardcoded palette): the trigger is the
// brand entry point, a filter is a warning-colored decision, an action is informing
// the world. Icon chip + a left accent bar carry the color; text stays foreground
// so the node is readable at every zoom.
const NODE_ACCENT: Record<FlowCanvasNode["kind"], { bar: string; chip: string }> = {
  trigger: { bar: "bg-brand", chip: "bg-brand/15 text-brand" },
  filter: { bar: "bg-warning", chip: "bg-warning/15 text-warning" },
  action: { bar: "bg-info", chip: "bg-info/15 text-info" },
};

export function AutomationFlowCanvas({
  nodes,
  selectedId,
  onSelect,
  onOpen,
  onInsert,
  onReorder,
  onRemove,
  disabled,
}: AutomationFlowCanvasProps) {
  const { t } = useT("automations");
  const frameRef = useRef<HTMLDivElement | null>(null);
  const [viewport, setViewport] = useState<Viewport>({ x: 0, y: 0, zoom: 1 });
  const [fullscreen, setFullscreen] = useState(false);
  const [drag, setDrag] = useState<DragState | null>(null);
  const [spaceHeld, setSpaceHeld] = useState(false);
  const panRef = useRef<{ pointerId: number; startX: number; startY: number; originX: number; originY: number } | null>(null);

  // Derived layout: one vertical lane, recomputed on every change so adding or
  // removing a step can never leave a stale coordinate.
  const positions = useMemo(
    () => nodes.map((_, index) => ({ x: CANVAS_PAD, y: CANVAS_PAD + index * (NODE_HEIGHT + NODE_GAP) })),
    [nodes],
  );
  const contentWidth = CANVAS_PAD * 2 + NODE_WIDTH;
  const contentHeight = CANVAS_PAD * 2 + Math.max(nodes.length, 1) * (NODE_HEIGHT + NODE_GAP) - NODE_GAP;

  // The OPENING view is zoom 1, top-centered — draw.io's rule that "zooming does
  // not change the diagram" cuts both ways: a fresh flow must not open shrunk to
  // 46% just to prove it fits. Fit stays one keystroke away (1) for long flows.
  const home = useCallback(() => {
    const frame = frameRef.current?.getBoundingClientRect();
    if (!frame || frame.width === 0) return;
    setViewport({ x: (frame.width - contentWidth) / 2, y: 12, zoom: 1 });
  }, [contentWidth]);

  const fit = useCallback(() => {
    const frame = frameRef.current?.getBoundingClientRect();
    if (!frame || frame.width === 0) return;
    const zoom = Math.min(1, Math.max(MIN_ZOOM, Math.min(frame.width / contentWidth, frame.height / contentHeight)));
    setViewport({
      x: (frame.width - contentWidth * zoom) / 2,
      y: Math.max(12, (frame.height - contentHeight * zoom) / 2),
      zoom,
    });
  }, [contentHeight, contentWidth]);

  useEffect(() => {
    home();
    // Re-home when the frame itself changes size (fullscreen toggle).
  }, [home, fullscreen]);

  // A newly added node must be reachable: if the chain grew past the frame, keep
  // the view where it is (the insert affordance the user clicked stays put) — no
  // forced re-fit that would shrink everything mid-edit.

  const zoomBy = useCallback((factor: number, anchor?: { x: number; y: number }) => {
    setViewport((current) => {
      const next = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, current.zoom * factor));
      if (next === current.zoom) return current;
      const frame = frameRef.current?.getBoundingClientRect();
      const focusX = anchor && frame ? anchor.x - frame.left : (frame?.width ?? 0) / 2;
      const focusY = anchor && frame ? anchor.y - frame.top : (frame?.height ?? 0) / 2;
      const ratio = next / current.zoom;
      return { zoom: next, x: focusX - (focusX - current.x) * ratio, y: focusY - (focusY - current.y) * ratio };
    });
  }, []);

  const onKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    const target = event.target as HTMLElement | null;
    if (target && (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.tagName === "SELECT")) return;

    switch (event.key) {
      case " ":
        setSpaceHeld(true);
        event.preventDefault();
        return;
      case "+":
      case "=":
        zoomBy(ZOOM_STEP);
        return;
      case "-":
      case "_":
        zoomBy(1 / ZOOM_STEP);
        return;
      case "0":
        home();
        return;
      case "1":
        fit();
        return;
      case "Escape":
        if (fullscreen) setFullscreen(false);
        return;
      case "Enter":
        if (onOpen && selectedId !== "") openNode(selectedId);
        return;
      case "Delete":
      case "Backspace": {
        if (disabled || !onRemove || selectedId === "trigger" || selectedId === "") return;
        const index = Number(selectedId);
        if (Number.isInteger(index)) {
          event.preventDefault();
          onRemove(index);
        }
        return;
      }
      default:
        return;
    }
  };

  // Opening a node's parameters exits fullscreen first: the panel lives beside
  // the canvas, and an "open" that changes nothing visible reads as a dead key.
  const openNode = (id: string) => {
    if (!onOpen) return;
    setFullscreen(false);
    onOpen(id);
  };

  const onWheel = (event: React.WheelEvent<HTMLDivElement>) => {
    if (event.ctrlKey || event.metaKey) {
      event.preventDefault();
      zoomBy(event.deltaY < 0 ? ZOOM_STEP : 1 / ZOOM_STEP, { x: event.clientX, y: event.clientY });
      return;
    }
    // A plain wheel scrolls the lane — draw.io's default vertical scroll.
    event.preventDefault();
    setViewport((current) => ({ ...current, x: current.x - event.deltaX, y: current.y - event.deltaY }));
  };

  const startPan = (event: React.PointerEvent<HTMLDivElement>) => {
    const onBackground = event.target === event.currentTarget;
    const panGesture = event.button === 1 || spaceHeld || event.metaKey || event.ctrlKey;
    if (!onBackground && !panGesture) return;
    panRef.current = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      originX: viewport.x,
      originY: viewport.y,
    };
    event.currentTarget.setPointerCapture?.(event.pointerId);
  };

  const movePan = (event: React.PointerEvent<HTMLDivElement>) => {
    const pan = panRef.current;
    if (!pan || pan.pointerId !== event.pointerId) return;
    setViewport((current) => ({
      ...current,
      x: pan.originX + (event.clientX - pan.startX),
      y: pan.originY + (event.clientY - pan.startY),
    }));
  };

  const endPan = (event: React.PointerEvent<HTMLDivElement>) => {
    if (panRef.current?.pointerId === event.pointerId) panRef.current = null;
  };

  // Node dragging = reordering along the vertical lane. The drop index is read off
  // the pointer's Y in CONTENT space (correct at any zoom) and shown live.
  const dropIndexForClientY = useCallback(
    (clientY: number) => {
      const frame = frameRef.current?.getBoundingClientRect();
      if (!frame) return 0;
      const contentY = (clientY - frame.top - viewport.y) / viewport.zoom;
      const slot = Math.round((contentY - CANVAS_PAD) / (NODE_HEIGHT + NODE_GAP)) - 1;
      return Math.min(Math.max(slot, 0), Math.max(nodes.length - 2, 0));
    },
    [nodes.length, viewport.y, viewport.zoom],
  );

  const startNodeDrag = (event: React.PointerEvent<HTMLDivElement>, stepIndex: number) => {
    if (disabled || !onReorder) return;
    event.stopPropagation();
    (event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId);
    setDrag({ pointerId: event.pointerId, stepIndex, startClientY: event.clientY, offsetY: 0, dropIndex: stepIndex });
  };

  const moveNodeDrag = (event: React.PointerEvent<HTMLDivElement>) => {
    setDrag((current) => {
      if (!current || current.pointerId !== event.pointerId) return current;
      return { ...current, offsetY: event.clientY - current.startClientY, dropIndex: dropIndexForClientY(event.clientY) };
    });
  };

  const endNodeDrag = (event: React.PointerEvent<HTMLDivElement>) => {
    setDrag((current) => {
      if (!current || current.pointerId !== event.pointerId) return current;
      if (onReorder && current.dropIndex !== current.stepIndex) onReorder(current.stepIndex, current.dropIndex);
      return null;
    });
  };

  return (
    <div
      className={cn(
        "relative overflow-hidden border bg-background",
        fullscreen ? "fixed inset-0 z-50" : "rounded-lg",
      )}
      style={fullscreen ? undefined : { height: "min(58vh, 520px)" }}
    >
      <div
        ref={frameRef}
        role="application"
        tabIndex={0}
        aria-label={t(($) => $.flow.canvas_label)}
        className={cn(
          "absolute inset-0 touch-none outline-none",
          spaceHeld ? "cursor-grab active:cursor-grabbing" : "cursor-default",
        )}
        style={{
          backgroundImage: "radial-gradient(color-mix(in oklch, var(--color-foreground) 12%, transparent) 1px, transparent 1px)",
          backgroundSize: `${20 * viewport.zoom}px ${20 * viewport.zoom}px`,
          backgroundPosition: `${viewport.x}px ${viewport.y}px`,
        }}
        onWheel={onWheel}
        onKeyDown={onKeyDown}
        onKeyUp={(event) => {
          if (event.key === " ") setSpaceHeld(false);
        }}
        onBlur={() => setSpaceHeld(false)}
        onPointerDown={startPan}
        onPointerMove={movePan}
        onPointerUp={endPan}
        onPointerCancel={endPan}
      >
        <div
          className="absolute left-0 top-0 origin-top-left"
          style={{
            transform: `translate(${viewport.x}px, ${viewport.y}px) scale(${viewport.zoom})`,
            width: contentWidth,
            height: contentHeight,
          }}
        >
          {/* Edges under the nodes: straight verticals with an arrowhead, the
              flowchart idiom rather than the graph-tool bezier. */}
          <svg
            className="pointer-events-none absolute left-0 top-0 overflow-visible"
            width={contentWidth}
            height={contentHeight}
            aria-hidden
          >
            <defs>
              <marker id="flow-arrow" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                <path d="M 0 0 L 8 4 L 0 8 z" fill="var(--color-muted-foreground)" />
              </marker>
            </defs>
            {positions.slice(0, -1).map((from, index) => {
              const to = positions[index + 1];
              if (!to) return null;
              const x = from.x + NODE_WIDTH / 2;
              return (
                <line
                  key={index}
                  x1={x}
                  y1={from.y + NODE_HEIGHT}
                  x2={x}
                  y2={to.y - 3}
                  stroke="var(--color-muted-foreground)"
                  strokeOpacity={0.55}
                  strokeWidth={1.75}
                  markerEnd="url(#flow-arrow)"
                />
              );
            })}
            {drag && (
              <rect
                x={CANVAS_PAD - 8}
                y={CANVAS_PAD + (drag.dropIndex + 1) * (NODE_HEIGHT + NODE_GAP) - NODE_GAP / 2 - 2}
                width={NODE_WIDTH + 16}
                height={4}
                rx={2}
                className="fill-brand"
              />
            )}
          </svg>

          {nodes.map((node, index) => {
            const position = positions[index];
            if (!position) return null;
            const selected = node.id === selectedId;
            const stepIndex = node.kind === "trigger" ? -1 : index - 1;
            const dragging = drag?.stepIndex === stepIndex && stepIndex >= 0;
            const accent = NODE_ACCENT[node.kind];
            return (
              <div
                key={node.id}
                className="absolute"
                style={{
                  left: position.x,
                  top: position.y,
                  transform: dragging ? `translateY(${(drag?.offsetY ?? 0) / viewport.zoom}px)` : undefined,
                  zIndex: dragging ? 20 : 1,
                }}
                onPointerDown={stepIndex >= 0 ? (event) => startNodeDrag(event, stepIndex) : undefined}
                onPointerMove={stepIndex >= 0 ? moveNodeDrag : undefined}
                onPointerUp={stepIndex >= 0 ? endNodeDrag : undefined}
                onPointerCancel={stepIndex >= 0 ? endNodeDrag : undefined}
              >
                <button
                  type="button"
                  onClick={() => onSelect(node.id)}
                  onDoubleClick={() => openNode(node.id)}
                  aria-pressed={selected}
                  className={cn(
                    "relative flex items-center gap-3 overflow-hidden rounded-lg border bg-card px-3 text-left shadow-sm transition",
                    dragging ? "cursor-grabbing opacity-90 shadow-lg" : "cursor-grab",
                    selected ? "border-brand ring-2 ring-brand/25" : "border-border hover:border-foreground/25",
                  )}
                  style={{ width: NODE_WIDTH, height: NODE_HEIGHT }}
                >
                  <span className={cn("absolute inset-y-0 left-0 w-1", accent.bar)} aria-hidden />
                  {node.outcome && (
                    <span
                      title={node.outcomeLabel}
                      className={cn(
                        "absolute right-2 top-2 size-2 rounded-full",
                        node.outcome === "ok" && "bg-success",
                        node.outcome === "failed" && "bg-destructive",
                        node.outcome === "stopped" && "bg-warning",
                        node.outcome === "not_run" && "bg-muted-foreground/30",
                      )}
                    />
                  )}
                  <span className={cn("flex size-8 shrink-0 items-center justify-center rounded-md", accent.chip)}>
                    {node.kind === "trigger" ? (
                      <Zap className="size-4" aria-hidden />
                    ) : node.kind === "filter" ? (
                      <Filter className="size-4" aria-hidden />
                    ) : (
                      <Workflow className="size-4" aria-hidden />
                    )}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                      {node.kicker}
                    </span>
                    <span className="block truncate text-sm font-medium text-foreground">{node.title}</span>
                    {node.subtitle !== "" && (
                      <span className="block truncate text-xs text-muted-foreground">{node.subtitle}</span>
                    )}
                  </span>
                </button>

                {/* The "+" on the outgoing edge, like n8n's connector plus. */}
                {onInsert && !disabled && (
                  <Button
                    size="icon-sm"
                    variant="outline"
                    aria-label={t(($) => $.flow.add_step)}
                    className="absolute size-6 rounded-full bg-background shadow-sm"
                    style={{ left: NODE_WIDTH / 2 - 12, top: NODE_HEIGHT + NODE_GAP / 2 - 12 }}
                    onPointerDown={(event) => event.stopPropagation()}
                    onClick={() => onInsert(index)}
                  >
                    <Plus className="size-3" aria-hidden />
                  </Button>
                )}
              </div>
            );
          })}
        </div>
      </div>

      {/* Fullscreen toggle, top-right — draw.io's placement. */}
      <Button
        size="icon-sm"
        variant="outline"
        aria-label={fullscreen ? t(($) => $.flow.exit_fullscreen) : t(($) => $.flow.fullscreen)}
        className="absolute right-3 top-3 bg-background/95 shadow-sm"
        onClick={() => setFullscreen((current) => !current)}
      >
        {fullscreen ? <Minimize2 aria-hidden /> : <Maximize2 aria-hidden />}
      </Button>

      {/* Zoom cluster, bottom-left — n8n's placement. */}
      <div className="absolute bottom-3 left-3 flex items-center gap-1 rounded-md border bg-background/95 p-1 shadow-sm">
        <Button size="icon-sm" variant="ghost" aria-label={t(($) => $.flow.zoom_out)} onClick={() => zoomBy(1 / ZOOM_STEP)}>
          <Minus aria-hidden />
        </Button>
        <span className="min-w-10 text-center text-xs tabular-nums text-muted-foreground">
          {Math.round(viewport.zoom * 100)}%
        </span>
        <Button size="icon-sm" variant="ghost" aria-label={t(($) => $.flow.zoom_in)} onClick={() => zoomBy(ZOOM_STEP)}>
          <Plus aria-hidden />
        </Button>
        <Button size="icon-sm" variant="ghost" aria-label={t(($) => $.flow.fit)} onClick={fit}>
          <Scan aria-hidden />
        </Button>
      </div>

      <p className="pointer-events-none absolute bottom-3 right-3 hidden text-[11px] text-muted-foreground sm:block">
        {t(($) => $.flow.canvas_hint)}
      </p>
    </div>
  );
}
