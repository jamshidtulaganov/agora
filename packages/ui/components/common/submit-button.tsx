"use client";

import type { ReactNode } from "react";
import { ArrowUp, Loader2, Square } from "lucide-react";
import { Button } from "@agora/ui/components/ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@agora/ui/components/ui/tooltip";

interface SubmitButtonProps {
  onClick: () => void;
  disabled?: boolean;
  loading?: boolean;
  running?: boolean;
  onStop?: () => void;
  /**
   * Tooltip shown over the send button when idle. Pass a string or a node
   * (e.g. `Send · ⌘↵`). Omit to render no tooltip.
   * Callers compose the shortcut hint themselves to keep this component
   * free of `@agora/core` (platform-detection) and i18n imports.
   */
  tooltip?: ReactNode;
  /** Tooltip shown over the stop button while a run is in progress. */
  stopTooltip?: ReactNode;
  /** Accessible name for the idle send button (icon-only otherwise). */
  label?: string;
  /** Accessible name for the stop button while a run is in progress. */
  stopLabel?: string;
}

function SubmitButton({
  onClick,
  disabled,
  loading,
  running,
  onStop,
  tooltip,
  stopTooltip,
  label = "Send",
  stopLabel = "Stop",
}: SubmitButtonProps) {
  if (running) {
    const stopButton = (
      <Button size="icon-sm" onClick={onStop} aria-label={stopLabel}>
        <Square className="fill-current" />
      </Button>
    );
    if (!stopTooltip) return stopButton;
    return (
      <Tooltip>
        <TooltipTrigger render={stopButton} />
        <TooltipContent side="top">{stopTooltip}</TooltipContent>
      </Tooltip>
    );
  }

  const submitButton = (
    <Button
      size="icon-sm"
      disabled={disabled || loading}
      onClick={onClick}
      aria-label={label}
    >
      {loading ? <Loader2 className="animate-spin" /> : <ArrowUp />}
    </Button>
  );
  if (!tooltip) return submitButton;
  return (
    <Tooltip>
      <TooltipTrigger render={submitButton} />
      <TooltipContent side="top">{tooltip}</TooltipContent>
    </Tooltip>
  );
}

export { SubmitButton, type SubmitButtonProps };
