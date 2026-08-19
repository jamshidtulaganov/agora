"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Filter, Maximize2, Minus, Plus, Workflow, Zap } from "lucide-react";
import { Button } from "@agora/ui/components/ui/button";
import { useT } from "../../i18n";

// The flow canvas — modelled on n8n's editor so the interactions are the ones a
// person already has in their fingers (docs.n8n.io/build/keyboard-shortcuts):
//
//   pan      background drag, middle-drag, space+drag, ⌘/ctrl+drag, trackpad swipe
//   zoom     ⌘/ctrl + wheel (around the cursor), +/-, buttons
//   0        reset zoom      1  zoom to fit
//   Enter    open the selected node's parameters
//   Delete   remove the selected step
//
// Two deliberate departures from n8n, both because our flow is a LINEAR chain
// rather than a free graph:
//
//  1. Dragging a node REORDERS it instead of leaving it wherever it was dropped.
//     In n8n the edges define execution order, so a position is cosmetic; here the
//     order IS the model, so a node parked off-lane would be a lie about what runs
//     next. Dragging therefore snaps back into the lane at the index it was
//     dropped at, with a live indicator showing where it will land.
//  2. Positions are derived, never stored, so a saved rule carries no coordinates
//     that could drift out of agreement with its steps.

const NODE_WIDTH = 210;
const NODE_HEIGHT = 92;
const NODE_GAP = 72;
const CANVAS_PAD = 56;
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
}

interface AutomationFlowCanvasProps {
  nodes: FlowCanvasNode[];
  selectedId: string;
  onSelect: (id: string) => void;
  /** Open the node's parameters (double-click / Enter). */
  onOpen?: (id: string) => void;
  /** Insert a step at this index (0 = directly after the trigger). */
  onInsert?: (index: number) => void;
  /** Move the step at `from` to `to` (both are step indexes, trigger excluded). */
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
  startClientX: number;
  offsetX: number;
  dropIndex: number;
}

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
  const [drag, setDrag] = useState<DragState | null>(null);
  const [spaceHeld, setSpaceHeld] = useState(false);
  const panRef = useRef<{ pointerId: number; startX: number; startY: number; originX: number; originY: number } | null>(null);

  // Derived layout: one horizontal lane, evenly spaced, recomputed on every
  // change so adding or removing a step can never leave a stale coordinate.
  const positions = useMemo(
    () => nodes.map((_, index) => ({ x: CANVAS_PAD + index * (NODE_WIDTH + NODE_GAP), y: CANVAS_PAD })),
    [nodes],
  );
  const contentWidth = CANVAS_PAD * 2 + Math.max(nodes.length, 1) * (NODE_WIDTH + NODE_GAP);
  const contentHeight = CANVAS_PAD * 2 + NODE_HEIGHT;

  const fit = useCallback(() => {
    const frame = frameRef.current?.getBoundingClientRect();
    if (!frame || frame.width === 0) return;
    const zoom = Math.min(1, Math.min(frame.width / contentWidth, frame.height / contentHeight));
    setViewport({
      x: (frame.width - contentWidth * zoom) / 2,
      y: (frame.height - contentHeight * zoom) / 2,
      zoom,
    });
  }, [contentHeight, contentWidth]);

  // Fit on mount and whenever the chain's length changes, so a newly added step
  // is never off-screen.
  useEffect(() => {
    fit();
  }, [fit, nodes.length]);

  const zoomBy = useCallback((factor: number, anchor?: { x: number; y: number }) => {
    setViewport((current) => {
      const next = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, current.zoom * factor));
      if (next === current.zoom) return current;
      const frame = frameRef.current?.getBoundingClientRect();
      // Zoom around the cursor when there is one, else the frame centre, so what
      // is under the pointer stays under it.
      const focusX = anchor && frame ? anchor.x - frame.left : (frame?.width ?? 0) / 2;
      const focusY = anchor && frame ? anchor.y - frame.top : (frame?.height ?? 0) / 2;
      const ratio = next / current.zoom;
      return { zoom: next, x: focusX - (focusX - current.x) * ratio, y: focusY - (focusY - current.y) * ratio };
    });
  }, []);

  // Keyboard: n8n's own bindings, scoped to the canvas so they never fight the
  // name field or the parameter panel.
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
        setViewport((current) => ({ ...current, zoom: 1 }));
        return;
      case "1":
        fit();
        return;
      case "Enter":
        if (onOpen && selectedId !== "") onOpen(selectedId);
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

  const onWheel = (event: React.WheelEvent<HTMLDivElement>) => {
    if (event.ctrlKey || event.metaKey) {
      event.preventDefault();
      zoomBy(event.deltaY < 0 ? ZOOM_STEP : 1 / ZOOM_STEP, { x: event.clientX, y: event.clientY });
      return;
    }
    event.preventDefault();
    setViewport((current) => ({ ...current, x: current.x - event.deltaX, y: current.y - event.deltaY }));
  };

  const startPan = (event: React.PointerEvent<HTMLDivElement>) => {
    // Background drag, middle-drag, space+drag and ⌘/ctrl+drag all pan — the four
    // gestures n8n accepts. A drag that starts on a node does not.
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

  // ── Node dragging = reordering ────────────────────────────────────────────
  // The drop index is read off the pointer's position along the lane, in CONTENT
  // space (so it is correct at any zoom), and shown live as an insertion marker.
  const dropIndexForClientX = useCallback(
    (clientX: number) => {
      const frame = frameRef.current?.getBoundingClientRect();
      if (!frame) return 0;
      const contentX = (clientX - frame.left - viewport.x) / viewport.zoom;
      const slot = Math.round((contentX - CANVAS_PAD) / (NODE_WIDTH + NODE_GAP)) - 1;
      return Math.min(Math.max(slot, 0), Math.max(nodes.length - 2, 0));
    },
    [nodes.length, viewport.x, viewport.zoom],
  );

  const startNodeDrag = (event: React.PointerEvent<HTMLDivElement>, stepIndex: number) => {
    if (disabled || !onReorder) return;
    event.stopPropagation();
    (event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId);
    setDrag({
      pointerId: event.pointerId,
      stepIndex,
      startClientX: event.clientX,
      offsetX: 0,
      dropIndex: stepIndex,
    });
  };

  const moveNodeDrag = (event: React.PointerEvent<HTMLDivElement>) => {
    setDrag((current) => {
      if (!current || current.pointerId !== event.pointerId) return current;
      return {
        ...current,
        offsetX: event.clientX - current.startClientX,
        dropIndex: dropIndexForClientX(event.clientX),
      };
    });
  };

  const endNodeDrag = (event: React.PointerEvent<HTMLDivElement>) => {
    setDrag((current) => {
      if (!current || current.pointerId !== event.pointerId) return current;
      if (onReorder && current.dropIndex !== current.stepIndex) {
        onReorder(current.stepIndex, current.dropIndex);
      }
      return null;
    });
  };

  return (
    <div className="relative overflow-hidden rounded-lg border bg-muted/20" style={{ height: "min(58vh, 520px)" }}>
      <div
        ref={frameRef}
        role="application"
        tabIndex={0}
        aria-label={t(($) => $.flow.canvas_label)}
        className={[
          "absolute inset-0 touch-none outline-none",
          spaceHeld ? "cursor-grab active:cursor-grabbing" : "cursor-default",
        ].join(" ")}
        style={{
          backgroundImage: "radial-gradient(currentColor 1px, transparent 1px)",
          backgroundSize: `${18 * viewport.zoom}px ${18 * viewport.zoom}px`,
          backgroundPosition: `${viewport.x}px ${viewport.y}px`,
          color: "var(--color-border)",
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
          {/* Edges under the nodes, drawn output-dot → input-dot like n8n. */}
          <svg
            className="pointer-events-none absolute left-0 top-0 overflow-visible"
            width={contentWidth}
            height={contentHeight}
            aria-hidden
          >
            {positions.slice(0, -1).map((from, index) => {
              const to = positions[index + 1];
              if (!to) return null;
              const startX = from.x + NODE_WIDTH;
              const endX = to.x;
              const y = from.y + NODE_HEIGHT / 2;
              const mid = startX + (endX - startX) / 2;
              return (
                <path
                  key={index}
                  d={`M ${startX} ${y} C ${mid} ${y}, ${mid} ${y}, ${endX} ${y}`}
                  fill="none"
                  stroke="var(--color-border)"
                  strokeWidth={2}
                />
              );
            })}
            {/* Live insertion marker while a node is being dragged. */}
            {drag && (
              <rect
                x={CANVAS_PAD + (drag.dropIndex + 1) * (NODE_WIDTH + NODE_GAP) - NODE_GAP / 2 - 2}
                y={CANVAS_PAD - 8}
                width={4}
                height={NODE_HEIGHT + 16}
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
            return (
              <div
                key={node.id}
                className="absolute"
                style={{
                  left: position.x,
                  top: position.y,
                  transform: dragging ? `translateX(${(drag?.offsetX ?? 0) / viewport.zoom}px)` : undefined,
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
                  onDoubleClick={() => onOpen?.(node.id)}
                  aria-pressed={selected}
                  className={[
                    "flex flex-col justify-center gap-0.5 rounded-xl border bg-card px-3 text-left shadow-sm transition",
                    dragging ? "cursor-grabbing opacity-90 shadow-lg" : "cursor-grab",
                    selected ? "border-brand ring-2 ring-brand/30" : "hover:border-foreground/20",
                  ].join(" ")}
                  style={{ width: NODE_WIDTH, height: NODE_HEIGHT }}
                >
                  <span className="flex items-center gap-2">
                    <span
                      className={
                        node.kind === "trigger"
                          ? "flex size-6 shrink-0 items-center justify-center rounded-md bg-brand/10 text-brand"
                          : "flex size-6 shrink-0 items-center justify-center rounded-md border bg-background text-muted-foreground"
                      }
                    >
                      {node.kind === "trigger" ? (
                        <Zap className="size-3" aria-hidden />
                      ) : node.kind === "filter" ? (
                        <Filter className="size-3" aria-hidden />
                      ) : (
                        <Workflow className="size-3" aria-hidden />
                      )}
                    </span>
                    <span className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                      {node.kicker}
                    </span>
                  </span>
                  <span className="truncate text-sm font-medium">{node.title}</span>
                  <span className="truncate text-xs text-muted-foreground">{node.subtitle}</span>
                </button>

                {/* Output dot + its insert affordance, n8n's "+ on the connector". */}
                {onInsert && !disabled && (
                  <Button
                    size="icon-sm"
                    variant="outline"
                    aria-label={t(($) => $.flow.add_step)}
                    className="absolute size-6 rounded-full bg-background"
                    style={{ left: NODE_WIDTH + NODE_GAP / 2 - 12, top: NODE_HEIGHT / 2 - 12 }}
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

      {/* Zoom controls, bottom-left as in n8n. */}
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
          <Maximize2 aria-hidden />
        </Button>
      </div>

      <p className="pointer-events-none absolute bottom-3 right-3 text-[11px] text-muted-foreground">
        {t(($) => $.flow.canvas_hint)}
      </p>
    </div>
  );
}
