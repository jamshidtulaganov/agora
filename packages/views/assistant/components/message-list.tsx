"use client";

import { Fragment, useEffect, useMemo, useState } from "react";
import { Wrench, AlertCircle, Check, Copy, RotateCcw } from "lucide-react";
import { Button } from "@agora/ui/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@agora/ui/components/ui/tooltip";
import { copyText } from "@agora/ui/lib/clipboard";
import { cn } from "@agora/ui/lib/utils";
import type { AssistantMessage } from "@agora/core/types";
import { Markdown } from "../../common/markdown";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";
import { AssistantAvatar } from "./assistant-avatar";
import { ArtifactCard } from "./artifact-card";
import { humanizeToolName, isToolResultError, summarizeToolResult } from "../lib/tool-summary";
import { isArtifactToolName, parseArtifactToolResult } from "../lib/artifact";
import { isDayBoundary, relativeDay } from "../lib/transcript-days";
import {
  operationOutcomeAfter,
  parseConfirmationRequest,
  parseReceipt,
  parseUncertainOutcome,
} from "../lib/operation";
import { ConfirmCard } from "./confirm-card";
import { ReceiptChip, UncertainChip } from "./outcome-chips";

interface MessageListProps {
  messages: AssistantMessage[];
  /** Opens an artifact produced in this session. Omitted where there is
   *  nowhere to open it — the cards then render static. */
  onOpenArtifact?: (artifactId: string) => void;
  /** Re-sends the last user message. Offered on the LAST assistant row only
   *  (ChatGPT-style) and omitted while a run is active, which is what keeps
   *  the action from racing the reply it would replace. */
  onRegenerate?: () => void;
}

/** The row a regenerate action belongs on: the final assistant turn that
 *  actually said something. Pure tool-call turns render nothing. */
function lastSpokenAssistantId(messages: AssistantMessage[]): string | null {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index]!;
    if (message.role === "assistant" && message.content.trim()) return message.id;
  }
  return null;
}

export function MessageList({ messages, onOpenArtifact, onRegenerate }: MessageListProps) {
  // A confirmation card has to know whether a LATER row already reported its
  // outcome, so every row needs its position in the flat transcript — the
  // grouping below loses that.
  const indexById = useMemo(() => {
    const map = new Map<string, number>();
    messages.forEach((message, index) => map.set(message.id, index));
    return map;
  }, [messages]);

  const regenerateId = onRegenerate ? lastSpokenAssistantId(messages) : null;

  const row = (message: AssistantMessage) => (
    <MessageRow
      key={message.id}
      message={message}
      messages={messages}
      index={indexById.get(message.id) ?? -1}
      onOpenArtifact={onOpenArtifact}
      onRegenerate={message.id === regenerateId ? onRegenerate : undefined}
    />
  );

  const groups = groupMessages(messages);

  return (
    <div className="mx-auto flex w-full max-w-2xl flex-col gap-4 px-4 py-5">
      {groups.map((group, index) => {
        const previous = groups[index - 1];
        const showDaySeparator = isDayBoundary(
          previous?.[previous.length - 1]?.created_at,
          group[0]!.created_at,
        );
        return (
          <Fragment key={group[0]!.id}>
            {showDaySeparator && <DaySeparator iso={group[0]!.created_at} />}
            {group.length > 1 ? (
              // A run that called several tools in a row is ONE action by the
              // model; stacking those chips tight (gap-1 inside the
              // transcript's gap-4) reads as a single step instead of four
              // separate turns.
              <div className="flex flex-col gap-1">{group.map(row)}</div>
            ) : (
              row(group[0]!)
            )}
          </Fragment>
        );
      })}
    </div>
  );
}

/**
 * Hairline date divider, shown only where the reader's local day changed
 * between two rows (lib/transcript-days.ts). Today and yesterday are named;
 * anything older is formatted in the reader's own locale.
 */
function DaySeparator({ iso }: { iso: string }) {
  const { t, i18n } = useT("assistant");
  const relative = relativeDay(iso);
  const label =
    relative === "today"
      ? t(($) => $.transcript.today)
      : relative === "yesterday"
        ? t(($) => $.transcript.yesterday)
        : formatDay(iso, i18n.language);

  return (
    <div className="flex items-center gap-3" role="separator" aria-label={label}>
      <span className="h-px flex-1 bg-border" />
      <span className="shrink-0 text-[11px] text-muted-foreground">{label}</span>
      <span className="h-px flex-1 bg-border" />
    </div>
  );
}

function formatDay(iso: string, locale: string): string {
  const date = new Date(iso);
  const options: Intl.DateTimeFormatOptions = { month: "short", day: "numeric" };
  if (date.getFullYear() !== new Date().getFullYear()) options.year = "numeric";
  return date.toLocaleDateString(locale, options);
}

/** Consecutive `tool` rows become one cluster; everything else stays alone. */
function groupMessages(messages: AssistantMessage[]): AssistantMessage[][] {
  const groups: AssistantMessage[][] = [];
  for (const message of messages) {
    const last = groups[groups.length - 1];
    if (message.role === "tool" && last && last[0]!.role === "tool") {
      last.push(message);
    } else {
      groups.push([message]);
    }
  }
  return groups;
}

function MessageRow({
  message,
  messages,
  index,
  onOpenArtifact,
  onRegenerate,
}: {
  message: AssistantMessage;
  /** Full transcript — a confirmation row reads its outcome from later rows. */
  messages: AssistantMessage[];
  index: number;
  onOpenArtifact?: (artifactId: string) => void;
  /** Set on the last assistant row only. */
  onRegenerate?: () => void;
}) {
  if (message.role === "tool") {
    // Every branch below decodes an OPTIONAL tool_result shape and falls back
    // to the generic chip when it doesn't match, so a server that predates
    // (or outgrows) any of these still renders a readable transcript.
    // Artifact tools get a card instead of a chip — but only when the result
    // is the shape we know; anything else degrades to the generic chip.
    const artifact = isArtifactToolName(message.tool_name)
      ? parseArtifactToolResult(message.tool_result)
      : null;
    if (artifact) {
      return (
        <ArtifactCard
          artifact={artifact}
          onOpen={onOpenArtifact ? () => onOpenArtifact(artifact.artifactId) : undefined}
        />
      );
    }

    const confirmation = parseConfirmationRequest(message.tool_result);
    if (confirmation) {
      return (
        <ConfirmCard
          sessionId={message.session_id}
          request={confirmation}
          outcome={operationOutcomeAfter(messages, index, confirmation.operationId)}
        />
      );
    }

    const receipt = parseReceipt(message.tool_result);
    if (receipt) return <ReceiptChip receipt={receipt} />;

    const uncertain = parseUncertainOutcome(message.tool_result);
    if (uncertain) return <UncertainChip outcome={uncertain} />;

    return <ToolChip message={message} />;
  }

  if (message.role === "user") {
    return (
      <div className="group/msg flex flex-col items-end gap-1">
        <div className="max-w-[80%] whitespace-pre-wrap break-words rounded-2xl bg-muted px-3.5 py-2 text-sm">
          {message.content}
        </div>
        <MessageActions content={message.content} align="end" />
      </div>
    );
  }

  // Assistant role. A pure tool-call turn (the model asked for a tool but
  // said nothing) has empty content — the tool_calls it made render as their
  // own subsequent "tool" rows, so there is nothing to show here.
  if (!message.content.trim()) return null;

  return (
    <div className="group/msg flex items-start gap-2">
      <AssistantAvatar className="mt-0.5" />
      <div className="min-w-0 flex-1 rounded-2xl bg-transparent text-sm leading-relaxed">
        <div className="prose prose-sm dark:prose-invert max-w-none [&>*:first-child]:mt-0 [&>*:last-child]:mb-0">
          <Markdown>{message.content}</Markdown>
        </div>
        <MessageActions content={message.content} align="start" onRegenerate={onRegenerate} />
      </div>
    </div>
  );
}

/**
 * Quiet hover actions under a transcript row (ChatGPT-style). Copy only, for
 * now — the assistant's copy is the RAW markdown, not the rendered text, so
 * it pastes into an issue or a doc the way the model wrote it.
 *
 * Hidden until the row is hovered or something inside it takes focus, and
 * permanently visible on coarse pointers, where there is no hover to reveal it.
 */
function MessageActions({
  content,
  align,
  onRegenerate,
}: {
  content: string;
  align: "start" | "end";
  onRegenerate?: () => void;
}) {
  const { t } = useT("assistant");
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(false), 1500);
    return () => clearTimeout(timer);
  }, [copied]);

  const label = copied ? t(($) => $.message_actions.copied) : t(($) => $.message_actions.copy);
  const regenerateLabel = t(($) => $.message_actions.regenerate);

  return (
    <div
      className={cn(
        "flex opacity-0 transition-opacity focus-within:opacity-100 group-focus-within/msg:opacity-100 group-hover/msg:opacity-100 pointer-coarse:opacity-100",
        align === "end" ? "justify-end" : "-ml-1.5 justify-start",
      )}
    >
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={label}
              className="size-6 text-muted-foreground"
              onClick={() => {
                void copyText(content).then((ok) => {
                  if (ok) setCopied(true);
                });
              }}
            />
          }
        >
          {copied ? <Check className="size-3" /> : <Copy className="size-3" />}
        </TooltipTrigger>
        <TooltipContent side="bottom">{label}</TooltipContent>
      </Tooltip>
      {onRegenerate && (
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={regenerateLabel}
                className="size-6 text-muted-foreground"
                onClick={onRegenerate}
              />
            }
          >
            <RotateCcw className="size-3" />
          </TooltipTrigger>
          <TooltipContent side="bottom">{regenerateLabel}</TooltipContent>
        </Tooltip>
      )}
    </div>
  );
}

/**
 * Compact action chip for a `role: "tool"` row — icon + humanized tool name
 * + a best-effort one-line summary pulled from the (tool-specific) result
 * JSON. Never rendered as a chat bubble: tool turns are the assistant's
 * *actions*, not its words.
 */
function ToolChip({ message }: { message: AssistantMessage }) {
  const { t } = useT("assistant");
  const toolName = message.tool_name ?? "";
  const isError = isToolResultError(message.tool_result);
  const summary = summarizeToolResult(message.tool_result) ?? t(($) => $.tool_chip.fallback_summary);
  const linkHref = toolResultLink(message.tool_result);

  const chip = (
    <div
      className={cn(
        "ml-8 flex min-w-0 items-center gap-1.5 rounded-md border border-transparent px-2 py-1 text-xs text-muted-foreground",
        linkHref && "hover:border-border hover:bg-accent/40 transition-colors",
      )}
    >
      {isError ? (
        <AlertCircle className="size-3 shrink-0 text-destructive/80" />
      ) : (
        <Wrench className="size-3 shrink-0" />
      )}
      <span className="shrink-0 font-medium text-foreground/80">{humanizeToolName(toolName)}</span>
      <span className={cn("truncate", isError && "text-destructive/80")}>{summary}</span>
    </div>
  );

  if (linkHref) {
    return (
      <AppLink href={linkHref} className="inline-block no-underline">
        {chip}
      </AppLink>
    );
  }
  return chip;
}

/** A tool result carrying a workspace-relative `url` field becomes a link
 *  into the issue/entity it just touched (e.g. `create_issue`, `get_issue`). */
function toolResultLink(result: unknown): string | null {
  if (!result || typeof result !== "object") return null;
  const url = (result as Record<string, unknown>).url;
  return typeof url === "string" && url.startsWith("/") ? url : null;
}
