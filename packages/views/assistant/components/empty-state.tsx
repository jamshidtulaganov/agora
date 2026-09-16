"use client";

import { BarChart3, ListTodo, Plus, Activity } from "lucide-react";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";

/**
 * Pre-conversation launcher blocks. Deliberately NOT the icon-title-pills
 * empty-state kit: the composer itself is the hero of this screen — the page
 * composes mark → greeting → composer → prompt rows, so this file exports the
 * two blocks around the composer instead of one monolithic empty state.
 */

/** Identity block above the hero composer: the Agora mark + one-line invite.
 *  `compact` is the floating-panel form — smaller mark and type, tighter gap. */
export function AssistantHero({ compact }: { compact?: boolean } = {}) {
  const { t } = useT("assistant");
  return (
    <div
      className={cn(
        "flex flex-col items-center text-center",
        compact ? "mb-4 gap-2" : "mb-6 gap-3",
      )}
    >
      <AgoraIcon noSpin className={cn("text-foreground/70", compact ? "size-7" : "size-9")} />
      <div>
        <h2
          className={cn(
            "font-semibold tracking-tight",
            compact ? "text-lg" : "text-2xl",
          )}
        >
          {t(($) => $.empty_state.title)}
        </h2>
        <p
          className={cn(
            "mt-1.5 text-muted-foreground",
            compact ? "text-xs" : "text-sm",
          )}
        >
          {t(($) => $.empty_state.subtitle)}
        </p>
      </div>
    </div>
  );
}

interface PromptRowsProps {
  /** Prefills the composer with the example — does not send it. */
  onPickPrompt: (content: string) => void;
  /** Single column + smaller type for the floating panel. */
  compact?: boolean;
}

/**
 * Example prompts below the hero composer — quiet, left-aligned rows a user
 * scans like a menu. Each is a real task with the glyph of the surface it
 * touches, not a uniform pill.
 */
export function AssistantPromptRows({ onPickPrompt, compact }: PromptRowsProps) {
  const { t } = useT("assistant");

  const prompts = [
    { icon: Plus, text: t(($) => $.empty_state.prompts.create_issue) },
    { icon: ListTodo, text: t(($) => $.empty_state.prompts.my_plate) },
    { icon: BarChart3, text: t(($) => $.empty_state.prompts.usage_this_week) },
    { icon: Activity, text: t(($) => $.empty_state.prompts.today_digest) },
  ];

  return (
    <div
      className={cn(
        "mx-auto mt-4 grid w-full max-w-2xl grid-cols-1 gap-0.5",
        !compact && "sm:grid-cols-2",
      )}
    >
      {prompts.map(({ icon: Icon, text }) => (
        <button
          key={text}
          type="button"
          onClick={() => onPickPrompt(text)}
          className={cn(
            "flex min-w-0 items-center gap-2.5 rounded-lg text-left text-muted-foreground transition-colors hover:bg-accent/60 hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
            compact ? "px-2.5 py-2 text-xs" : "px-3 py-2.5 text-sm",
          )}
        >
          <Icon className="size-3.5 shrink-0 opacity-60" />
          <span className="truncate">{text}</span>
        </button>
      ))}
    </div>
  );
}
