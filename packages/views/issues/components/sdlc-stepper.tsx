"use client";

// The SDLC stepper strip — Design → Dev → QA → Review — mounted via
// CockpitFrame's `topStrip` slot. Renders the pipeline `deriveStagePipeline`
// already computed; owns no state of its own beyond click handling, which it
// delegates entirely to the caller (lens switching lives in lens.ts).
// See docs/sdlc-stage-cockpit-plan.md section 1/3 (phase C). Deploy is not a
// stage here — it moved to the sprint level (qa-sprint-readiness-view.tsx).

import { CheckCircle2, XCircle, TriangleAlert } from "lucide-react";
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
}

function stateTextClass(state: StageState): string {
  switch (state) {
    case "passed":
      return "text-emerald-600 dark:text-emerald-400";
    case "failed":
      return "text-destructive";
    case "blocked":
      return "text-amber-600 dark:text-amber-400";
    case "skipped":
      return "text-muted-foreground";
    case "active":
    case "running":
      return "text-foreground font-medium";
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
      // No looping animation on failed/blocked — alarm fatigue. Only the
      // color transition (from the flip wrapper + transition-colors) eases.
      return (
        <XCircle aria-hidden className="size-3.5 shrink-0 text-destructive transition-colors duration-300" />
      );
    case "blocked":
      return (
        <TriangleAlert
          aria-hidden
          className="size-3.5 shrink-0 text-amber-600 transition-colors duration-300 dark:text-amber-400"
        />
      );
    case "running":
      // info/blue is this codebase's "running / live" idiom (qa-lane.tsx,
      // live-agent-code-editor.tsx, editor-workbench.tsx) — not the
      // emerald "passed" tint, so running and passed read as distinct
      // states. Pulsing dot + a soft expanding ring behind it; the ring is
      // Tailwind's built-in `animate-ping`, gated `motion-safe:` so a
      // prefers-reduced-motion user gets a still, translucent halo.
      return (
        <span aria-hidden className="relative inline-flex size-2.5 shrink-0 items-center justify-center">
          <span className="absolute inline-flex size-2.5 rounded-full bg-info opacity-40 motion-safe:animate-ping" />
          <span className="relative inline-flex size-1.5 rounded-full bg-info transition-colors duration-300 motion-safe:animate-pulse" />
        </span>
      );
    case "active":
      // Gentle breathing halo (custom `sdlc-breathe` keyframe, see
      // packages/ui/styles/base.css) around the current-stage dot — a slow
      // ~2s scale/opacity cycle, calmer than the running ping so the two
      // "alive" states don't compete for attention.
      return (
        <span aria-hidden className="relative inline-flex size-2.5 shrink-0 items-center justify-center">
          <span className="absolute inline-flex size-4 rounded-full bg-primary/20 motion-safe:animate-sdlc-breathe" />
          <span className="relative inline-flex size-2.5 rounded-full bg-primary ring-2 ring-primary/30 transition-colors duration-300" />
        </span>
      );
    case "skipped":
      return (
        <span
          aria-hidden
          className="size-2.5 shrink-0 rounded-full bg-muted-foreground/30 transition-colors duration-300"
        />
      );
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
 *  - info-tinted + shimmer: this segment leads into the *current* stage —
 *    "work is flowing forward into this stage now." The shimmer is a
 *    `motion-safe:`-gated sweep (see `sdlc-connector-shimmer` in
 *    packages/ui/styles/base.css); `bg-info/30` alone is the static
 *    fallback so reduced-motion users still see which segment is "next".
 */
function connectorClassName(prevState: StageState, leadsIntoCurrent: boolean): string {
  if (leadsIntoCurrent) {
    return "bg-info/30 motion-safe:animate-sdlc-connector-shimmer";
  }
  if (prevState === "passed") {
    return "bg-emerald-500/40 dark:bg-emerald-500/35";
  }
  return "bg-border";
}

function stageLabel(stage: SDLCStage, t: ReturnType<typeof useT<"issues">>["t"]): string {
  switch (stage) {
    case "design":
      return t(($) => $.sdlc.design);
    case "dev":
      return t(($) => $.sdlc.dev);
    case "qa":
      return t(($) => $.sdlc.qa);
    case "review":
      return t(($) => $.sdlc.review);
  }
}

export function SDLCStepper({ pipeline, activeLens, isLensAvailable, onSelectStage }: SDLCStepperProps) {
  const { t } = useT("issues");

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

        // Remounting the dot on `state` change (key={snapshot.state}) is
        // what makes `animate-sdlc-flip` replay on a real transition
        // (e.g. QA running -> passed via WS invalidation) instead of only
        // on first mount — same technique as animate-onboarding-enter.
        const dot = (
          <span
            key={snapshot.state}
            aria-hidden
            className="inline-flex shrink-0 items-center motion-safe:animate-sdlc-flip"
          >
            <StageDot state={snapshot.state} />
          </span>
        );
        const labelSpan = (
          <span className={cn("transition-colors duration-300", stateTextClass(snapshot.state))}>{label}</span>
        );

        return (
          <div key={snapshot.stage} className="flex shrink-0 items-center">
            {i > 0 && (
              <span
                aria-hidden
                className={cn(
                  "mx-1.5 h-px w-4 shrink-0 transition-colors duration-300",
                  connectorClassName(prevState, snapshot.stage === pipeline.current),
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
                  selected && "underline decoration-2 underline-offset-4",
                )}
              >
                {dot}
                {labelSpan}
                {snapshot.detail && (
                  <span className="text-[10px] uppercase tracking-wide text-muted-foreground">
                    {snapshot.detail}
                  </span>
                )}
              </button>
            ) : (
              <div
                data-testid={`sdlc-stage-${snapshot.stage}`}
                data-state={snapshot.state}
                className={cn(
                  "flex cursor-default items-center gap-1.5 rounded-md px-1.5 py-1 transition-colors duration-300",
                  snapshot.state === "skipped" && "opacity-40",
                  selected && "underline decoration-2 underline-offset-4",
                )}
              >
                {dot}
                {labelSpan}
                {snapshot.detail && (
                  <span className="text-[10px] uppercase tracking-wide text-muted-foreground">
                    {snapshot.detail}
                  </span>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
