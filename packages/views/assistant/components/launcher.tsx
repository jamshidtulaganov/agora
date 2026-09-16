"use client";

import type { ReactNode } from "react";
import { cn } from "@agora/ui/lib/utils";
import { Composer } from "./composer";
import { AssistantHero, AssistantPromptRows } from "./empty-state";

interface AssistantLauncherProps {
  value: string;
  onValueChange: (v: string) => void;
  onSend: (content: string) => void;
  isSending?: boolean;
  sendUnavailable?: boolean;
  scopeLabel?: string;
  /** The session's focus workspace — see components/context-chip.tsx. Absent
   *  before a session exists, where there is nothing to re-scope yet. */
  contextChip?: ReactNode;
  /** Tightened spacing + single-column prompts for the floating panel. */
  compact?: boolean;
}

/**
 * The pre-conversation screen: the composer IS the hero, centered in the
 * canvas — mark, one-line invite, the input, then example prompts. Once the
 * first message lands the conversation view takes over and the same composer
 * docks to the bottom (its "docked" variant).
 *
 * Shared by the full page and the floating panel so the two never fork —
 * `compact` only changes spacing, never behavior.
 */
export function AssistantLauncher({
  value,
  onValueChange,
  onSend,
  isSending,
  sendUnavailable,
  scopeLabel,
  contextChip,
  compact,
}: AssistantLauncherProps) {
  return (
    <div
      className={cn(
        "flex min-h-0 flex-1 items-center justify-center overflow-y-auto",
        compact ? "px-3 py-4" : "px-4 py-8",
      )}
    >
      <div className={cn("w-full max-w-2xl", !compact && "-mt-12")}>
        <AssistantHero compact={compact} />
        <Composer
          variant="hero"
          value={value}
          onValueChange={onValueChange}
          onSend={onSend}
          isSending={isSending}
          sendUnavailable={sendUnavailable}
          scopeLabel={scopeLabel}
          contextChip={contextChip}
        />
        <AssistantPromptRows onPickPrompt={onValueChange} compact={compact} />
      </div>
    </div>
  );
}
