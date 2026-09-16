"use client";

import { useQuery } from "@tanstack/react-query";
import { cn } from "@agora/ui/lib/utils";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { Tooltip, TooltipContent, TooltipTrigger } from "@agora/ui/components/ui/tooltip";
import {
  assistantAvailabilityOptions,
  useAssistantPanelStore,
  assistantSessionListOptions,
} from "@agora/core/assistant";
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
  const { t } = useT("assistant");
  const isOpen = useAssistantPanelStore((s) => s.isOpen);
  const toggle = useAssistantPanelStore((s) => s.toggle);
  const { pathname } = useNavigation();
  const { data: availability } = useQuery(assistantAvailabilityOptions());
  // Object identity from the store — stable between runs, so this is safe as
  // a selector (a freshly-built object would loop).
  const { data: sessions = [] } = useQuery(assistantSessionListOptions());

  if (availability?.enabled !== true) return null;
  if (isOpen) return null;
  if (isAssistantPage(pathname)) return null;

  const isRunning = sessions.some((session) => session.latest_run?.status === "queued" || session.latest_run?.status === "running");
  const tooltip = isRunning ? t(($) => $.fab.running) : t(($) => $.fab.default);

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
      </TooltipTrigger>
      <TooltipContent side="top" sideOffset={10}>
        {tooltip}
      </TooltipContent>
    </Tooltip>
  );
}
