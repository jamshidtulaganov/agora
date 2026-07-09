"use client";

// The SDLC stepper strip — Design → Dev → QA → Review → Deploy — mounted via
// CockpitFrame's `topStrip` slot. Renders the pipeline `deriveStagePipeline`
// already computed; owns no state of its own beyond click handling, which it
// delegates entirely to the caller (lens switching lives in lens.ts).
// See docs/sdlc-stage-cockpit-plan.md section 1/3 (phase C).

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
      return <CheckCircle2 aria-hidden className="size-3.5 shrink-0 text-emerald-600 dark:text-emerald-400" />;
    case "failed":
      return <XCircle aria-hidden className="size-3.5 shrink-0 text-destructive" />;
    case "blocked":
      return <TriangleAlert aria-hidden className="size-3.5 shrink-0 text-amber-600 dark:text-amber-400" />;
    case "running":
      return <span aria-hidden className="size-1.5 shrink-0 rounded-full bg-emerald-500 motion-safe:animate-pulse" />;
    case "active":
      return <span aria-hidden className="size-2.5 shrink-0 rounded-full bg-primary ring-2 ring-primary/30" />;
    case "skipped":
      return <span aria-hidden className="size-2.5 shrink-0 rounded-full bg-muted-foreground/30" />;
    case "pending":
    default:
      return <span aria-hidden className="size-2.5 shrink-0 rounded-full border border-muted-foreground/40" />;
  }
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
    case "deploy":
      return t(($) => $.sdlc.deploy);
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

        return (
          <div key={snapshot.stage} className="flex shrink-0 items-center">
            {i > 0 && <span aria-hidden className="mx-1.5 h-px w-4 shrink-0 bg-border" />}
            {interactive ? (
              <button
                type="button"
                data-testid={`sdlc-stage-${snapshot.stage}`}
                data-state={snapshot.state}
                onClick={() => onSelectStage(snapshot.stage)}
                className={cn(
                  "flex items-center gap-1.5 rounded-md px-1.5 py-1 transition-colors hover:bg-accent/60",
                  snapshot.state === "skipped" && "opacity-40",
                  selected && "underline decoration-2 underline-offset-4",
                )}
              >
                <StageDot state={snapshot.state} />
                <span className={stateTextClass(snapshot.state)}>{label}</span>
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
                  "flex cursor-default items-center gap-1.5 rounded-md px-1.5 py-1",
                  snapshot.state === "skipped" && "opacity-40",
                  selected && "underline decoration-2 underline-offset-4",
                )}
              >
                <StageDot state={snapshot.state} />
                <span className={stateTextClass(snapshot.state)}>{label}</span>
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
