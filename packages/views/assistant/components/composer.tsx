"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import type { KeyboardEvent, ReactNode } from "react";
import { Textarea } from "@agora/ui/components/ui/textarea";
import { cn } from "@agora/ui/lib/utils";
import { SubmitButton } from "@agora/ui/components/common/submit-button";
import { useT } from "../../i18n";
import { filterSlashCommands, opensSlashMenu } from "../lib/slash-commands";
import { SlashMenu, useSlashCommands } from "./slash-menu";
import type { SlashCommandItem } from "./slash-menu";

export const ASSISTANT_MESSAGE_MAX_LENGTH = 8000;

interface ComposerProps {
  onSend: (content: string) => void;
  onStop?: () => void;
  /** A run is active on this session — input locks, button becomes Stop. */
  isRunning?: boolean;
  /** The send request itself is in flight (pre-run-id ack). */
  isSending?: boolean;
  sendUnavailable?: boolean;
  /** Prefilled text (e.g. from an empty-state example prompt). */
  value: string;
  onValueChange: (value: string) => void;
  scopeLabel?: string;
  /**
   * The session's resolved context (focus workspace), rendered above the
   * field in BOTH variants. A node rather than a value so the composer stays
   * free of query/mutation wiring — see components/context-chip.tsx.
   */
  contextChip?: ReactNode;
  /**
   * "docked" (default) — the conventional bottom bar under a transcript.
   * "hero" — the launcher form of the same composer, rendered centered in
   * the canvas before a conversation exists: no top border, a roomier field.
   * One component for both so behavior (send, stop, limit) never forks.
   */
  variant?: "docked" | "hero";
}

const NEAR_LIMIT_THRESHOLD = 200;

export function Composer({
  onSend,
  onStop,
  isRunning,
  isSending,
  sendUnavailable,
  value,
  onValueChange,
  scopeLabel,
  contextChip,
  variant = "docked",
}: ComposerProps) {
  const { t } = useT("assistant");
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // --- slash commands ---------------------------------------------------
  // Opened only by "/" as the first character of an empty composer; further
  // typing filters it, and it closes as soon as nothing matches (so typing a
  // path like "/tmp/x" gets out of the way on its own).
  const commands = useSlashCommands();
  const [isSlashActive, setSlashActive] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);

  const matches = useMemo(
    () => (isSlashActive ? filterSlashCommands(commands, value.slice(1)) : []),
    [isSlashActive, commands, value],
  );
  const isMenuOpen = isSlashActive && matches.length > 0;

  // Filtering changed under the cursor — highlight the first match again.
  useEffect(() => {
    setActiveIndex(0);
  }, [value]);

  // Set after a template pick: the textarea only holds the template text on
  // the NEXT render, so the caret has to be placed then, not now.
  const placeCaretAtEndRef = useRef(false);
  useEffect(() => {
    if (!placeCaretAtEndRef.current) return;
    placeCaretAtEndRef.current = false;
    const el = textareaRef.current;
    if (!el) return;
    el.focus();
    el.setSelectionRange(el.value.length, el.value.length);
  }, [value]);

  const handleSend = () => {
    const trimmed = value.trim();
    if (!trimmed || isRunning || isSending || sendUnavailable) return;
    onSend(trimmed);
  };

  const handleChange = (next: string) => {
    const clipped = next.slice(0, ASSISTANT_MESSAGE_MAX_LENGTH);
    if (opensSlashMenu(value, clipped)) setSlashActive(true);
    // A newline means the user is writing a message, not picking a command.
    else if (isSlashActive && (!clipped.startsWith("/") || clipped.includes("\n"))) {
      setSlashActive(false);
    }
    onValueChange(clipped);
  };

  const handlePickCommand = (command: SlashCommandItem) => {
    setSlashActive(false);
    if (command.action === "send") {
      if (isRunning || isSending || sendUnavailable) return;
      onValueChange(command.payload);
      onSend(command.payload);
      return;
    }
    placeCaretAtEndRef.current = true;
    onValueChange(command.payload);
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (isMenuOpen) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setActiveIndex((index) => (index + 1) % matches.length);
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setActiveIndex((index) => (index - 1 + matches.length) % matches.length);
        return;
      }
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        const command = matches[activeIndex] ?? matches[0];
        if (command) handlePickCommand(command);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setSlashActive(false);
        return;
      }
    }

    // Enter sends, Shift+Enter inserts a newline — this composer is a plain
    // textarea (no rich-text list/quote continuation to protect), so a bare
    // Enter is free to mean "send".
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const remaining = ASSISTANT_MESSAGE_MAX_LENGTH - value.length;
  const nearLimit = remaining <= NEAR_LIMIT_THRESHOLD;

  const hero = variant === "hero";

  const field = (
    <div className="relative mx-auto w-full max-w-2xl">
      {isMenuOpen && (
        <SlashMenu
          commands={matches}
          activeIndex={Math.min(activeIndex, matches.length - 1)}
          onHover={setActiveIndex}
          onPick={handlePickCommand}
        />
      )}
      <div
        className={cn(
          "flex w-full items-end gap-2 rounded-xl border border-input bg-card transition-colors focus-within:border-brand/60",
          hero ? "p-2.5 shadow-sm" : "p-2",
        )}
      >
        <Textarea
          ref={textareaRef}
          value={value}
          onChange={(e) => handleChange(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={t(($) => $.composer.placeholder)}
          maxLength={ASSISTANT_MESSAGE_MAX_LENGTH}
          rows={hero ? 2 : 1}
          autoFocus={hero}
          className={cn(
            "flex-1 resize-none border-0 bg-transparent px-1 py-1 shadow-none outline-none focus-visible:ring-0 dark:bg-transparent",
            hero ? "max-h-48 min-h-14" : "max-h-40 min-h-9",
          )}
        />
        <SubmitButton
          onClick={handleSend}
          disabled={!value.trim() || isSending || sendUnavailable}
          loading={isSending}
          running={isRunning}
          onStop={onStop}
          tooltip={t(($) => $.composer.send_tooltip)}
          stopTooltip={t(($) => $.composer.stop_tooltip)}
        />
      </div>
    </div>
  );

  const context = contextChip ? (
    <div className="mx-auto mb-1.5 flex w-full max-w-2xl items-center">{contextChip}</div>
  ) : null;

  const counter = nearLimit && (
    <div className="mx-auto mt-1 w-full max-w-2xl text-right text-xs text-muted-foreground">
      {t(($) => $.composer.char_limit, { count: value.length, max: ASSISTANT_MESSAGE_MAX_LENGTH })}
    </div>
  );

  if (hero) {
    return (
      <div className="w-full">
        {context}
        {field}
        {scopeLabel && <div className="mx-auto mt-2 w-full max-w-2xl text-xs text-muted-foreground">{scopeLabel}</div>}
        {counter}
      </div>
    );
  }

  return (
    <div className="border-t bg-background px-4 py-3">
      {context}
      {field}
      {scopeLabel && <div className="mx-auto mt-2 w-full max-w-2xl text-xs text-muted-foreground">{scopeLabel}</div>}
      {counter}
    </div>
  );
}
