"use client";

import type { ReactNode } from "react";
import { cn } from "@agora/ui/lib/utils";
import { Composer } from "./composer";
import { useAssistantComposeResources } from "./compose-resources";
import type { ComposeResources } from "./compose-resources";
import { AssistantHero, AssistantPromptRows } from "./empty-state";

interface AssistantLauncherProps {
  value: string;
  onValueChange: (v: string) => void;
  onSend: (content: string) => void;
  isSending?: boolean;
  sendUnavailable?: boolean;
  status?: string;
  /** The session's focus workspace — see components/context-chip.tsx. Absent
   *  before a session exists, where there is nothing to re-scope yet. */
  contextChip?: ReactNode;
  /** Message context from useAssistantComposeResources. */
  resources?: ComposeResources;
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
  status,
  contextChip,
  resources,
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
          status={status}
          contextChip={contextChip}
          toolbar={resources?.toolbar}
          attachments={resources?.attachments}
          notices={resources?.notices}
        />
        <AssistantPromptRows onPickPrompt={onValueChange} compact={compact} />
      </div>
    </div>
  );
}

/**
 * The launcher before any session exists: the message context is kept under a
 * placeholder session key until the first send creates the real session.
 * Shared by the full page and the floating panel.
 */
export function AssistantDraftLauncher({
  draftKey,
  workspaceId,
  onUploadingChange,
  ...launcher
}: Omit<AssistantLauncherProps, "resources" | "status" | "contextChip"> & {
  draftKey: string;
  workspaceId: string | null;
  onUploadingChange: (uploading: boolean) => void;
}) {
  const resources = useAssistantComposeResources({ sessionId: draftKey, workspaceId, onUploadingChange });
  return <AssistantLauncher {...launcher} resources={resources} />;
}
