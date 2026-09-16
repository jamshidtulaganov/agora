"use client";

import { useCallback, useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { cn } from "@agora/ui/lib/utils";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { Tooltip, TooltipContent, TooltipTrigger } from "@agora/ui/components/ui/tooltip";
import {
  assistantAvailabilityOptions,
  useAssistantPanelStore,
  assistantSessionListOptions,
} from "@agora/core/assistant";
import { useWSEvent } from "@agora/core/realtime";
import type { AssistantRunFinishedPayload } from "@agora/core/types";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";

/** Trailing slashes are legal in both routers — normalize before matching. */
function isAssistantPage(pathname: string): boolean {
  return pathname.replace(/\/+$/, "").endsWith("/assistant");
}

/**
 * The bottom-right bubble. One job: open the Assistant panel. Hidden when the
 * panel is already open, when the user is already on the full assistant page
 * (redundant there), and when the instance reports the assistant unavailable.
 */
export function AssistantFab() {
  const isOpen = useAssistantPanelStore((s) => s.isOpen);
  const clearUnseenResult = useAssistantPanelStore((s) => s.clearUnseenResult);
  const { pathname } = useNavigation();
  const { data: availability } = useQuery(assistantAvailabilityOptions());

  const onAssistantPage = isAssistantPage(pathname);

  // Both of these ARE seeing the result: the panel shows it, and the page is
  // the same conversation. Clearing here keeps the dot out of "stale badge"
  // territory without every surface having to remember to reset it.
  useEffect(() => {
    if (isOpen || onAssistantPage) clearUnseenResult();
  }, [isOpen, onAssistantPage, clearUnseenResult]);

  if (availability?.enabled !== true) return null;
  if (isOpen) return null;
  if (onAssistantPage) return null;

  return <AssistantFabButton />;
}

function AssistantFabButton() {
  const { t } = useT("assistant");
  const toggle = useAssistantPanelStore((s) => s.toggle);
  const hasUnseenResult = useAssistantPanelStore((s) => s.hasUnseenResult);
  const markUnseenResult = useAssistantPanelStore((s) => s.markUnseenResult);
  // Object identity from the store — stable between runs, so this is safe as
  // a selector (a freshly-built object would loop).
  const { data: sessions = [] } = useQuery(assistantSessionListOptions());

  // Ephemeral UI signal, not server state: "a reply landed while you weren't
  // looking". The cache stays the single source of truth for the reply itself
  // (ws-updaters.ts invalidates it) — this only decides whether to draw a dot,
  // and the local event time is what makes it immune to clock skew.
  const onRunFinished = useCallback(
    (payload: unknown) => {
      if ((payload as AssistantRunFinishedPayload | null)?.status === "ok") markUnseenResult();
    },
    [markUnseenResult],
  );
  useWSEvent("assistant:run_finished", onRunFinished);

  const isRunning = sessions.some((session) => session.latest_run?.status === "queued" || session.latest_run?.status === "running");
  const tooltip = isRunning
    ? t(($) => $.fab.running)
    : hasUnseenResult
      ? t(($) => $.fab.unseen)
      : t(($) => $.fab.default);

  return (
    <Tooltip>
      <TooltipTrigger
        onClick={toggle}
        aria-label={tooltip}
        className={cn(
          "absolute bottom-2 right-2 z-50 flex size-10 cursor-pointer items-center justify-center rounded-full bg-card text-foreground/70 shadow-sm ring-1 ring-foreground/10 transition-transform hover:scale-110 hover:text-foreground active:scale-95",
          // Impulse the button itself while a run is in flight — no outer
          // ring, to keep things calm.
          isRunning && "animate-chat-impulse",
        )}
      >
        <AgoraIcon noSpin className="size-5" />
        {hasUnseenResult && !isRunning && (
          <span
            aria-hidden
            className="absolute right-0.5 top-0.5 size-2 rounded-full bg-brand ring-2 ring-card"
          />
        )}
      </TooltipTrigger>
      <TooltipContent side="top" sideOffset={10}>
        {tooltip}
      </TooltipContent>
    </Tooltip>
  );
}
