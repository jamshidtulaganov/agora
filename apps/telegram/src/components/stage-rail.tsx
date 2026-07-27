import type { SDLCStage, StagePipeline, StageState } from "@agora/core/issues";
import { cn } from "../lib/cn";

// Renders the derived SDLC pipeline (Dev → Review) as the design's
// dot-and-connector rail. Two sizes: "sm" for task-list cards, "lg" for the
// detail screen's cycle-position card. A `done` issue renders the whole rail
// in success green ("Merged" treatment in the design). Design and QA both left
// the pipeline as stages — design is a dev-build input, QA is an on-demand
// action — see packages/core/issues/stage.ts.

export const STAGE_ORDER: SDLCStage[] = ["dev", "review"];

// Per-stage accent used for the *current* node, group swatches and the cycle
// distribution bar. Semantic tokens only — dark mode comes for free.
export const STAGE_DOT_BG: Record<SDLCStage, string> = {
  dev: "bg-warning",
  review: "bg-brand",
};

export const STAGE_TEXT: Record<SDLCStage, string> = {
  dev: "text-warning",
  review: "text-brand",
};

type NodeKind = "done" | "current" | "failed" | "pending" | "skipped";

function nodeKind(state: StageState, isCurrent: boolean): NodeKind {
  if (state === "skipped") return "skipped";
  if (state === "failed") return "failed";
  if (state === "passed") return "done";
  if (isCurrent || state === "active" || state === "running" || state === "blocked")
    return "current";
  return "pending";
}

function Node({
  kind,
  stage,
  allDone,
  size,
}: {
  kind: NodeKind;
  stage: SDLCStage;
  allDone: boolean;
  size: "sm" | "lg";
}) {
  const base = size === "sm" ? "size-2.5" : "size-3.5";
  const current = size === "sm" ? "size-3" : "size-[18px]";
  if (allDone) {
    return <span className={cn("shrink-0 rounded-full bg-success", base)} />;
  }
  switch (kind) {
    case "skipped":
      return (
        <span
          className={cn("box-border shrink-0 rounded-full border-2 border-dashed border-muted-foreground/40", base)}
        />
      );
    case "done":
      return <span className={cn("shrink-0 rounded-full bg-brand", base)} />;
    case "failed":
      return (
        <span
          className={cn(
            "shrink-0 rounded-full bg-destructive ring-[3px] ring-destructive/20",
            current,
          )}
        />
      );
    case "current":
      // Layered ping halo = "the cycle is alive here" (design: live running).
      return (
        <span className={cn("relative shrink-0", current)}>
          <span
            className={cn(
              "absolute inset-0 animate-ag-ping rounded-full",
              STAGE_DOT_BG[stage],
            )}
          />
          <span
            className={cn(
              "absolute inset-0 rounded-full ring-[3px]",
              STAGE_DOT_BG[stage],
              stage === "dev" && "ring-warning/20",
              stage === "review" && "ring-brand/20",
            )}
          />
        </span>
      );
    default:
      return <span className={cn("shrink-0 rounded-full bg-border", base)} />;
  }
}

export function StageRail({
  pipeline,
  done = false,
  size = "sm",
  className,
}: {
  pipeline: StagePipeline;
  /** Issue is done/merged — render the whole rail in success green. */
  done?: boolean;
  size?: "sm" | "lg";
  className?: string;
}) {
  const currentIdx = STAGE_ORDER.indexOf(pipeline.current);
  const connH = size === "sm" ? "h-0.5" : "h-[2.5px]";
  return (
    <div className={cn("flex min-w-0 flex-1 items-center", size === "sm" ? "gap-[3px]" : "gap-1", className)}>
      {pipeline.stages.map((s, i) => {
        const kind = nodeKind(s.state, i === currentIdx && !done);
        // Connector before node i takes the color of the segment it leads
        // into: done → brand, current → stage accent, else border gray.
        const connColor = done
          ? "bg-success"
          : kind === "done"
            ? "bg-brand"
            : kind === "current" || kind === "failed"
              ? STAGE_DOT_BG[s.stage]
              : "bg-border";
        return (
          <div key={s.stage} className="contents">
            {i > 0 && <span className={cn("min-w-1 flex-1 rounded-full", connH, connColor)} />}
            <Node kind={done ? "done" : kind} stage={s.stage} allDone={done} size={size} />
          </div>
        );
      })}
    </div>
  );
}

// Flex-weighted segment bar for the agent hero card (design 6a): each stage
// is a 5px rounded bar; completed = success, gated/active = warning shimmer.
export function StageSegments({
  pipeline,
  className,
}: {
  pipeline: StagePipeline;
  className?: string;
}) {
  const currentIdx = STAGE_ORDER.indexOf(pipeline.current);
  const weights: Record<SDLCStage, number> = { dev: 6, review: 4 };
  return (
    <div className={cn("flex items-center gap-[3px]", className)}>
      {pipeline.stages.map((s, i) => {
        const kind = nodeKind(s.state, i === currentIdx);
        const color =
          kind === "done"
            ? "bg-success"
            : kind === "failed"
              ? "bg-destructive"
              : kind === "current"
                ? "animate-ag-shimmer bg-warning"
                : "bg-border";
        return (
          <span
            key={s.stage}
            className={cn("h-[5px] rounded-[3px]", color)}
            style={{ flex: weights[s.stage] }}
          />
        );
      })}
    </div>
  );
}
