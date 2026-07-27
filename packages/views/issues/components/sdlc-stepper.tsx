"use client";

// The SDLC stepper strip — Dev → Review — mounted via CockpitFrame's
// `topStrip` slot. Renders the pipeline `deriveStagePipeline` already
// computed; owns no state of its own beyond click handling, which it
// delegates entirely to the caller (lens switching lives in lens.ts).
// See docs/sdlc-stage-cockpit-plan.md section 1/3 (phase C).
//
// Two beats, two owners: an agent builds, a human reviews and merges. QA,
// design and deploy are not stages here — QA is an on-demand action (its lens
// stays reachable at `?lens=qa`), design is an INPUT injected into the dev
// build task (`?lens=design`, see figma-links-section.tsx's "Open design view"
// entry point and lens.ts), and deploy moved to the sprint level
// (qa-sprint-readiness-view.tsx).

import { useEffect, useRef, type ReactNode } from "react";
import { CheckCircle2, XCircle } from "lucide-react";
import type { SDLCStage, StagePipeline, StageState } from "@agora/core/issues";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@agora/ui/components/ui/popover";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { StageLiveProcessBody } from "./stage-live-process";

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
      return "text-brand font-medium";
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
      // Active without a task means the pipeline is parked at this gate (for
      // example, waiting for human approval). A slow breath reads as "current"
      // without implying that compute is still running.
      return (
        <span aria-hidden className="relative inline-flex size-2.5 shrink-0 items-center justify-center">
          <span className="absolute inline-flex size-4 rounded-full bg-brand/20 motion-safe:animate-sdlc-breathe" />
          <span className="relative inline-flex size-2.5 rounded-full bg-brand ring-2 ring-brand/30 transition-colors duration-300" />
        </span>
      );
    case "running":
      // Running is deliberately distinct from merely active: the ring rotates
      // around a stable core, so a still screenshot remains legible while live
      // users also see continuous progress. `motion-safe` preserves the static
      // ring for people who request reduced motion.
      return (
        <span aria-hidden className="relative inline-flex size-4 shrink-0 items-center justify-center">
          <span className="absolute inset-0 rounded-full border border-brand/25 border-t-brand motion-safe:animate-sdlc-running-ring" />
          <span className="relative inline-flex size-1.5 rounded-full bg-brand shadow-[0_0_0_2px_color-mix(in_oklab,var(--brand)_18%,transparent)]" />
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
    return "bg-brand/30";
  }
  if (prevState === "passed") {
    return "bg-emerald-500/40 dark:bg-emerald-500/35";
  }
  return "bg-border";
}

function StageConnector({
  prevState,
  leadsIntoCurrent,
  currentState,
}: {
  prevState: StageState;
  leadsIntoCurrent: boolean;
  currentState: StageState;
}) {
  const flowing = leadsIntoCurrent && currentState === "running";
  return (
    <span
      aria-hidden
      data-flowing={flowing || undefined}
      className={cn(
        "relative mx-1.5 h-px w-4 shrink-0 overflow-hidden transition-colors duration-300",
        connectorClassName(prevState, leadsIntoCurrent, currentState),
      )}
    >
      {flowing && (
        <span className="absolute inset-y-0 left-0 w-1/2 bg-brand motion-safe:animate-sdlc-flow" />
      )}
    </span>
  );
}

function stateLabel(state: StageState, t: ReturnType<typeof useT<"issues">>["t"]): string {
  switch (state) {
    case "active":
      return t(($) => $.sdlc.state.active);
    case "running":
      return t(($) => $.sdlc.state.running);
    case "passed":
      return t(($) => $.sdlc.state.passed);
    case "failed":
      return t(($) => $.sdlc.state.failed);
    case "blocked":
      return t(($) => $.sdlc.state.blocked);
    case "skipped":
      return t(($) => $.sdlc.state.skipped);
    case "pending":
    default:
      return t(($) => $.sdlc.state.pending);
  }
}

function stageLabel(stage: SDLCStage, t: ReturnType<typeof useT<"issues">>["t"]): string {
  switch (stage) {
    case "dev":
      return t(($) => $.sdlc.dev);
    case "review":
      return t(($) => $.sdlc.review);
  }
}

export function SDLCStepper({ pipeline, activeLens, isLensAvailable, onSelectStage, trailing }: SDLCStepperProps) {
  const { t } = useT("issues");

  // Per-stage previous state, committed after each paint. The flip animation
  // plays ONLY on a genuine state change (e.g. dev running -> passed via a WS
  // invalidation), never on first mount — so loading an issue doesn't pop both
  // dots at once. `prev === undefined` (first render) => no flip.
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
        const accessibleLabel = `${label}: ${stateLabel(snapshot.state, t)}`;
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
        // The selected lens uses Agora's brand tint; the dot still communicates
        // workflow state independently (pass/fail/running/pending).
        const labelSpan = (
          <span
            className={cn(
              "transition-colors duration-300",
              stateTextClass(snapshot.state),
              selected && "text-brand",
            )}
          >
            {label}
          </span>
        );
        // No detail chip: "STALE"/"FULL"/tier were jargon, and the live states
        // ("merging…"/"awaiting approval") are already told by the dot + the
        // stage's own lens. The stepper stays a clean [dot] [label] beat.

        const stageClass = cn(
          "relative flex items-center gap-1.5 rounded-md px-1.5 py-1 outline-none transition-[color,background-color,box-shadow] duration-300 focus-visible:ring-2 focus-visible:ring-ring/60",
          snapshot.state === "skipped" && "opacity-40",
          snapshot.state === "running" && "bg-brand/[0.06] shadow-[inset_0_0_0_1px_color-mix(in_oklab,var(--brand)_12%,transparent)]",
          selected && "bg-brand/[0.08]",
        );

        return (
          <div key={snapshot.stage} className="flex shrink-0 items-center">
            {i > 0 && (
              <StageConnector
                prevState={prevState}
                leadsIntoCurrent={snapshot.stage === pipeline.current}
                currentState={snapshot.state}
              />
            )}
            {snapshot.taskId ? (
              // A task is running THIS stage — clicking it opens the live
              // process (what the agent is doing now), bound to the stage. The
              // full stage lens stays one click away inside the popover.
              <Popover>
                <PopoverTrigger
                  render={
                    <button
                      type="button"
                      data-testid={`sdlc-stage-${snapshot.stage}`}
                      data-state={snapshot.state}
                      aria-label={accessibleLabel}
                      aria-current={snapshot.stage === pipeline.current ? "step" : undefined}
                      className={cn(stageClass, "hover:bg-accent/60")}
                    />
                  }
                >
                  {dot}
                  {labelSpan}
                </PopoverTrigger>
                <PopoverContent align="start" className="w-80">
                  <StageLiveProcessBody taskId={snapshot.taskId} />
                  {interactive && (
                    <button
                      type="button"
                      onClick={() => onSelectStage(snapshot.stage)}
                      className="mt-2 flex w-full items-center justify-center rounded-md border border-border px-2 py-1 text-[11px] text-muted-foreground outline-none transition-colors hover:bg-accent hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/60"
                    >
                      {t(($) => $.live_activity.open_view, { stage: label })}
                    </button>
                  )}
                </PopoverContent>
              </Popover>
            ) : interactive ? (
              <button
                type="button"
                data-testid={`sdlc-stage-${snapshot.stage}`}
                data-state={snapshot.state}
                aria-label={accessibleLabel}
                aria-current={snapshot.stage === pipeline.current ? "step" : undefined}
                onClick={() => onSelectStage(snapshot.stage)}
                className={cn(stageClass, "hover:bg-accent/60")}
              >
                {dot}
                {labelSpan}
              </button>
            ) : (
              <div
                data-testid={`sdlc-stage-${snapshot.stage}`}
                data-state={snapshot.state}
                aria-label={accessibleLabel}
                aria-current={snapshot.stage === pipeline.current ? "step" : undefined}
                className={cn(stageClass, "cursor-default")}
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
