"use client";

import { Loader2 } from "lucide-react";
import { AssistantAvatar } from "./assistant-avatar";
import { humanizeToolName } from "../lib/tool-summary";
import { useT } from "../../i18n";

/**
 * Subtle "working" row shown while a run is active on the open session.
 * `activeTool` is the last `assistant:tool_activity` event's tool name
 * (ephemeral store state, see @agora/core/assistant) — null while the model
 * itself is still generating (no tool call in flight yet).
 */
export function WorkingIndicator({ activeTool }: { activeTool: string | null }) {
  const { t } = useT("assistant");
  const label = activeTool
    ? t(($) => $.working.with_tool, { tool: humanizeToolName(activeTool) })
    : t(($) => $.working.generic);

  return (
    <div className="mx-auto flex w-full max-w-2xl items-center gap-2 px-4 pb-3 text-sm text-muted-foreground">
      <AssistantAvatar />
      <Loader2 className="size-3.5 animate-spin" />
      <span>{label}</span>
    </div>
  );
}
