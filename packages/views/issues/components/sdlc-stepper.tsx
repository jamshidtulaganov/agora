"use client";

// The SDLC stepper strip — Dev → QA → Review — mounted via CockpitFrame's
// `topStrip` slot. Renders the pipeline `deriveStagePipeline` already
// computed; owns no state of its own beyond click handling, which it
// delegates entirely to the caller (lens switching lives in lens.ts).
// See docs/sdlc-stage-cockpit-plan.md section 1/3 (phase C). Deploy is not a
// stage here — it moved to the sprint level (qa-sprint-readiness-view.tsx).
// Design is not a stage here either — for Agora's ICP (small vibe-coding
// dev teams, usually without a dedicated designer) it's an INPUT injected
// into the dev build task, not a stepper stage with its own reviewer
// ceremony. The design lens/machinery stays reachable as an optional,
// deep-linkable view (`?lens=design`) for teams that want it — see
// figma-links-section.tsx's "Open design view" entry point and lens.ts.

import { useEffect, useRef, type ReactNode } from "react";
import { CheckCircle2, XCircle } from "lucide-react";
import type { SDLCStage, StagePipeline, StageState } from "@agora/core/issues";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";

export interface SDLCStepperProps {
  pipeline: StagePipeline;
  /** The currently-mounted lens key ("issue" | SDLCStage). Highlights the matching stage. */
  activeLens: string;
  /** Whether a lens is registered for this stage — gates click interactivity. */
  isLensAvailable: (stage: SDLCStage) => boolean;
  onSelectStage: (stage: SDLCStage) => void;
  /** Optional right-aligned slot — the orchestrator narrative (who owns this
   *  pipeline + its plain-language current action). Absent for human-run tasks. */
  trailing?: ReactNode;
}

// Four visual states only — the stepper is a glance, not a status console.
// blocked folds into failed (both are "stuck"); active + running fold into one
// "current / live" dot. skipped reuses the pending look (and the stage dims via
// opacity). This is the fold from 8 raw StageStates → 4 read-in-a-glance dots.
function stateTextClass(state: StageState): string {
  switch (state) {
    case "passed":
      return "text-emerald-600 dark:text-emerald-400";
    case "failed":
    case "blocked":
      return "text-destructive";
    case "active":
    case "running":
      return "text-foreground font-medium";
    case "skipped":
    case "pending":
    default:
      return "text-muted-foreground";
  }
}

function StageDot({ state }: { state: StageState }) {
  switch (state) {
    case "passed":
      return (
        <CheckCircle2
          aria-hidden
          className="size-3.5 shrink-0 text-emerald-600 transition-colors duration-300 dark:text-emerald-400"
        />
      );
    case "failed":
    case "blocked":
      // No looping animation on a stuck stage — alarm fatigue. Only the color
      // transition (from the flip wrapper + transition-colors) eases.
      return (
        <XCircle aria-hidden className="size-3.5 shrink-0 text-destructive transition-colors duration-300" />
      );
    case "active":
    case "running":
      // The single "current / live" dot: a gentle breathing halo (custom
      // `sdlc-breathe` keyframe, see packages/ui/styles/base.css). Whether the
      // stage is merely current or actively running, it reads as "this is where
      // work is now" — one calm animation, not two competing ones.
      return (
        <span aria-hidden className="relative inline-flex size-2.5 shrink-0 items-center justify-center">
          <span className="absolute inline-flex size-4 rounded-full bg-primary/20 motion-safe:animate-sdlc-breathe" />
          <span className="relative inline-flex size-2.5 rounded-full bg-primary ring-2 ring-primary/30 transition-colors duration-300" />
        </span>
      );
    case "skipped":
    case "pending":
    default:
      return (
        <span
          aria-hidden
          className="size-2.5 shrink-0 rounded-full border border-muted-foreground/40 transition-colors duration-300"
        />
      );
  }
}

/**
 * Connector line between stage i-1 and stage i.
 *  - `bg-border` (default): the segment hasn't been reached yet.
 *  - filled emerald: the prior stage passed — completed progress.
 *  - static info tint: this segment leads into the *current* stage AND that
 *    stage is live — "next up". No shimmer sweep: one moving animation (the
 *    breathing current dot) is enough; a second one competing on the connector
 *    was motion noise.
 *
 * A current stage that has FAILED or is BLOCKED gets no tint: work is not
 * flowing into it, it is stuck there.
 */
function connectorClassName(
  prevState: StageState,
  leadsIntoCurrent: boolean,
  currentState: StageState,
): string {
  const currentIsLive = currentState !== "failed" && currentState !== "blocked";
  if (leadsIntoCurrent && currentIsLive) {
    return "bg-info/30";
  }
  if (prevState === "passed") {
    return "bg-emerald-500/40 dark:bg-emerald-500/35";
  }
  return "bg-border";
}

function stageLabel(stage: SDLCStage, t: ReturnType<typeof useT<"issues">>["t"]): string {
  switch (stage) {
    case "dev":
      return t(($) => $.sdlc.dev);
    case "qa":
      return t(($) => $.sdlc.qa);
    case "review":
      return t(($) => $.sdlc.review);
  }
}

export function SDLCStepper({ pipeline, activeLens, isLensAvailable, onSelectStage, trailing }: SDLCStepperProps) {
  const { t } = useT("issues");

  // Per-stage previous state, committed after each paint. The flip animation
  // plays ONLY on a genuine state change (e.g. QA running -> passed via a WS
  // invalidation), never on first mount — so loading an issue doesn't pop all
  // four dots at once. `prev === undefined` (first render) => no flip.
  const prevStatesRef = useRef<Partial<Record<SDLCStage, StageState>>>({});
  useEffect(() => {
    const next: Partial<Record<SDLCStage, StageState>> = {};
    for (const s of pipeline.stages) next[s.stage] = s.state;
    prevStatesRef.current = next;
  });

  return (
    <div
      data-testid="sdlc-stepper"
      className="flex h-9 shrink-0 items-center gap-1 overflow-x-auto border-b px-4 text-xs"
    >
      {pipeline.stages.map((snapshot, i) => {
        const interactive = isLensAvailable(snapshot.stage);
        const selected = activeLens === snapshot.stage;
        const label = stageLabel(snapshot.stage, t);
        // `noUncheckedIndexedAccess` makes `stages[i - 1]` possibly
        // undefined even though `i > 0` guarantees it exists; the `??
        // "pending"` fallback is unreachable at runtime and only satisfies
        // the type checker.
        const prevState = pipeline.stages[i - 1]?.state ?? "pending";

        // Flip only when THIS stage actually changed state since the last
        // commit — not on first mount. key={snapshot.state} remounts the dot
        // on change so the keyframe replays; the flip class is withheld on
        // the initial render (prev undefined) so page load stays calm.
        const prevOwn = prevStatesRef.current[snapshot.stage];
        const justChanged = prevOwn !== undefined && prevOwn !== snapshot.state;
        const dot = (
          <span
            key={snapshot.state}
            aria-hidden
            className={cn(
              "inline-flex shrink-0 items-center",
              justChanged && "motion-safe:animate-sdlc-flip",
            )}
          >
            <StageDot state={snapshot.state} />
          </span>
        );
        // Underline (the "selected lens" indicator) marks the active lens.
        const labelSpan = (
          <span
            className={cn(
              "transition-colors duration-300",
              stateTextClass(snapshot.state),
              selected && "underline decoration-primary decoration-2 underline-offset-4",
            )}
          >
            {label}
          </span>
        );
        // No detail chip: "STALE"/"FULL"/tier were jargon, and the live states
        // ("merging…"/"awaiting approval") are already told by the dot + the
        // stage's own lens. The stepper stays a clean [dot] [label] beat.

        return (
          <div key={snapshot.stage} className="flex shrink-0 items-center">
            {i > 0 && (
              <span
                aria-hidden
                className={cn(
                  "mx-1.5 h-px w-4 shrink-0 transition-colors duration-300",
                  connectorClassName(prevState, snapshot.stage === pipeline.current, snapshot.state),
                )}
              />
            )}
            {interactive ? (
              <button
                type="button"
                data-testid={`sdlc-stage-${snapshot.stage}`}
                data-state={snapshot.state}
                onClick={() => onSelectStage(snapshot.stage)}
                className={cn(
                  "flex items-center gap-1.5 rounded-md px-1.5 py-1 transition-colors duration-300 hover:bg-accent/60",
                  snapshot.state === "skipped" && "opacity-40",
                  selected && "bg-accent/50",
                )}
              >
                {dot}
                {labelSpan}
              </button>
            ) : (
              <div
                data-testid={`sdlc-stage-${snapshot.stage}`}
                data-state={snapshot.state}
                className={cn(
                  "flex cursor-default items-center gap-1.5 rounded-md px-1.5 py-1 transition-colors duration-300",
                  snapshot.state === "skipped" && "opacity-40",
                  selected && "bg-accent/50",
                )}
              >
                {dot}
                {labelSpan}
              </div>
            )}
          </div>
        );
      })}
      {trailing && (
        <div className="ml-auto flex shrink-0 items-center pl-3">{trailing}</div>
      )}
    </div>
  );
}
