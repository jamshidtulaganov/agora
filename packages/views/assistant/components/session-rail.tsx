"use client";

import { useEffect, useRef, useState } from "react";
import { Plus, Pencil, Trash2, Loader2 } from "lucide-react";
import { cn } from "@agora/ui/lib/utils";
import { Button } from "@agora/ui/components/ui/button";
import type { AssistantSession } from "@agora/core/types";
import { useT, useTimeAgo } from "../../i18n";

interface SessionRailProps {
  sessions: AssistantSession[];
  activeSessionId: string | null;
  runningSessionIds: ReadonlySet<string>;
  onSelect: (id: string) => void;
  onCreate: () => void;
  onRename: (id: string, title: string) => void;
  onDelete: (id: string) => void;
}

export function SessionRail({
  sessions,
  activeSessionId,
  runningSessionIds,
  onSelect,
  onCreate,
  onRename,
  onDelete,
}: SessionRailProps) {
  const { t } = useT("assistant");
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [confirmingDeleteId, setConfirmingDeleteId] = useState<string | null>(null);

  return (
    <div className="flex h-full w-64 shrink-0 flex-col border-r">
      <div className="flex items-center justify-between px-3 py-3">
        <span className="text-sm font-medium text-foreground">{t(($) => $.session_rail.title)}</span>
        <Button
          type="button"
          size="icon-sm"
          variant="ghost"
          onClick={onCreate}
          aria-label={t(($) => $.session_rail.new_session)}
          title={t(($) => $.session_rail.new_session)}
        >
          <Plus />
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
        {sessions.length === 0 ? (
          <div className="px-2 py-4 text-center text-xs text-muted-foreground">
            {t(($) => $.session_rail.empty)}
          </div>
        ) : (
          <div className="flex flex-col gap-0.5">
            {sessions.map((session) => (
              <SessionRow
                key={session.id}
                session={session}
                isActive={session.id === activeSessionId}
                isRunning={runningSessionIds.has(session.id)}
                isRenaming={renamingId === session.id}
                isConfirmingDelete={confirmingDeleteId === session.id}
                onSelect={() => onSelect(session.id)}
                onStartRename={() => setRenamingId(session.id)}
                onSubmitRename={(title) => {
                  setRenamingId(null);
                  const trimmed = title.trim();
                  if (trimmed && trimmed !== session.title) onRename(session.id, trimmed);
                }}
                onCancelRename={() => setRenamingId(null)}
                onStartDelete={() => setConfirmingDeleteId(session.id)}
                onCancelDelete={() => setConfirmingDeleteId(null)}
                onConfirmDelete={() => {
                  setConfirmingDeleteId(null);
                  onDelete(session.id);
                }}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

interface SessionRowProps {
  session: AssistantSession;
  isActive: boolean;
  isRunning: boolean;
  isRenaming: boolean;
  isConfirmingDelete: boolean;
  onSelect: () => void;
  onStartRename: () => void;
  onSubmitRename: (title: string) => void;
  onCancelRename: () => void;
  onStartDelete: () => void;
  onCancelDelete: () => void;
  onConfirmDelete: () => void;
}

function SessionRow({
  session,
  isActive,
  isRunning,
  isRenaming,
  isConfirmingDelete,
  onSelect,
  onStartRename,
  onSubmitRename,
  onCancelRename,
  onStartDelete,
  onCancelDelete,
  onConfirmDelete,
}: SessionRowProps) {
  const { t } = useT("assistant");
  const timeAgo = useTimeAgo();
  const title = session.title.trim() || t(($) => $.session_rail.untitled);

  return (
    <div
      role="button"
      tabIndex={0}
      aria-current={isActive ? "true" : undefined}
      onClick={() => {
        if (isRenaming || isConfirmingDelete) return;
        onSelect();
      }}
      onKeyDown={(e) => {
        if (isRenaming || isConfirmingDelete) return;
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onSelect();
        }
      }}
      className={cn(
        "group/session-row relative flex min-h-11 min-w-0 cursor-default items-center gap-2 overflow-hidden rounded-md px-2 py-1.5 outline-none transition-colors hover:bg-accent/60 focus-visible:bg-accent/60",
        isActive && "bg-accent/70",
        isConfirmingDelete && "bg-destructive/5 hover:bg-destructive/5",
      )}
    >
      {isActive && <span className="absolute left-0 top-1.5 bottom-1.5 w-0.5 rounded-full bg-brand" />}
      <div className="min-w-0 flex-1">
        {isRenaming ? (
          <SessionRenameInput
            initialValue={session.title}
            onSubmit={onSubmitRename}
            onCancel={onCancelRename}
          />
        ) : isConfirmingDelete ? (
          <div className="truncate text-sm font-medium text-destructive">
            {t(($) => $.session_rail.delete_confirm_title)}
          </div>
        ) : (
          <div className="truncate text-sm">{title}</div>
        )}
        {!isRenaming && !isConfirmingDelete && (
          <div className="truncate text-xs text-muted-foreground">{timeAgo(session.updated_at)}</div>
        )}
      </div>
      {!isRenaming &&
        (isConfirmingDelete ? (
          <div className="flex shrink-0 items-center gap-1">
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                onCancelDelete();
              }}
              className="inline-flex h-7 items-center rounded px-2 text-[11px] font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              {t(($) => $.session_rail.delete_confirm_cancel)}
            </button>
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                onConfirmDelete();
              }}
              className="inline-flex h-7 items-center rounded px-2 text-[11px] font-medium text-destructive transition-colors hover:bg-destructive/10"
            >
              {t(($) => $.session_rail.delete_confirm_confirm)}
            </button>
          </div>
        ) : (
          <div className="flex shrink-0 items-center">
            {isRunning ? (
              <Loader2 className="size-3.5 shrink-0 animate-spin text-muted-foreground group-hover/session-row:hidden" />
            ) : null}
            <div className="hidden shrink-0 items-center gap-0.5 group-hover/session-row:flex">
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  onStartRename();
                }}
                className="inline-flex size-7 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                aria-label={t(($) => $.session_rail.rename_aria)}
                title={t(($) => $.session_rail.rename_aria)}
              >
                <Pencil className="size-3.5" />
              </button>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  onStartDelete();
                }}
                className="inline-flex size-7 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
                aria-label={t(($) => $.session_rail.delete_aria)}
                title={t(($) => $.session_rail.delete_aria)}
              >
                <Trash2 className="size-3.5" />
              </button>
            </div>
          </div>
        ))}
    </div>
  );
}

function SessionRenameInput({
  initialValue,
  onSubmit,
  onCancel,
}: {
  initialValue: string;
  onSubmit: (value: string) => void;
  onCancel: () => void;
}) {
  const [value, setValue] = useState(initialValue);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, []);

  return (
    <input
      ref={inputRef}
      value={value}
      onChange={(e) => setValue(e.target.value)}
      onClick={(e) => e.stopPropagation()}
      onBlur={() => onSubmit(value)}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          onSubmit(value);
        } else if (e.key === "Escape") {
          e.preventDefault();
          onCancel();
        }
      }}
      className="w-full rounded border border-input bg-background px-1.5 py-0.5 text-sm outline-none focus-visible:border-ring"
    />
  );
}
