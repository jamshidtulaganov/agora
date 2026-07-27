"use client";

import type { StagePipeline } from "@agora/core/issues";
import { useActorName } from "@agora/core/workspace/hooks";
import { ActorAvatar } from "../../common/actor-avatar";
import {
  Tooltip,
  TooltipTrigger,
  TooltipContent,
} from "@agora/ui/components/ui/tooltip";
import { useT } from "../../i18n";

/**
 * OrchestratorNarrative — the cockpit's "this pipeline is X's, and here's where
 * it is" header, mounted in the SDLCStepper's trailing slot. Renders nothing for
 * a human-run task (no agent orchestrator). Read-only: it reads the resolved
 * orchestrator + the already-derived stage pipeline, no new queries.
 *
 * The plain-language phrase is keyed off the current stage + its state. The
 * stepper dots already show pass/fail/running per stage; this is the one-line
 * "what the orchestrator is doing right now" a non-engineer reads at a glance —
 * one phrase per stage, plus the states that change the next move (a failed
 * gate, the review outcome).
 */
export function OrchestratorNarrative({
  pipeline,
  orchestratorAgentId,
}: {
  pipeline: StagePipeline;
  orchestratorAgentId: string | null | undefined;
}) {
  const { t } = useT("issues");
  const { getActorName } = useActorName();
  if (!orchestratorAgentId) return null;

  const state = pipeline.stages.find((s) => s.stage === pipeline.current)?.state ?? "pending";
  const detail = pipeline.stages.find((s) => s.stage === pipeline.current)?.detail;
  let phrase: string;
  switch (pipeline.current) {
    case "dev":
      phrase = t(($) => $.detail.narr_building);
      break;
    case "review":
      phrase =
        state === "failed"
          ? t(($) => $.detail.narr_changes)
          : state === "passed"
            ? t(($) => $.detail.narr_ready)
            : detail === "awaiting approval"
              ? t(($) => $.detail.narr_approval)
              : t(($) => $.detail.narr_reviewing);
      break;
    default:
      phrase = t(($) => $.detail.narr_working);
  }

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span className="flex items-center gap-1.5 whitespace-nowrap text-muted-foreground">
            <ActorAvatar actorType="agent" actorId={orchestratorAgentId} size={14} />
            <span className="max-w-[8rem] truncate">{getActorName("agent", orchestratorAgentId)}</span>
            <span aria-hidden>·</span>
            <span className="text-foreground">{phrase}</span>
          </span>
        }
      />
      <TooltipContent side="bottom">{t(($) => $.detail.prop_orchestrator_hint)}</TooltipContent>
    </Tooltip>
  );
}
