/* eslint-disable i18next/no-literal-string */
"use client";

// BitrixAssigneeChip surfaces the Bitrix task's responsible person on an issue
// that has no Agora assignee yet. The name/position are imported from Bitrix
// (user.get) and stored on the issue metadata as bitrix_responsible_name /
// bitrix_responsible_position — so the person who owns the task is visible even
// before they have an Agora account (at which point the email-match in the sync
// auto-promotes them to the real assignee and this chip disappears).
//
// Bitrix-specific copy is intentionally not translated (the panel opts out of
// the i18next/no-literal-string rule), matching the rest of the bitrix slice.

function str(meta: Record<string, unknown> | null | undefined, key: string): string {
  const v = meta?.[key];
  return typeof v === "string" ? v.trim() : "";
}

export function BitrixAssigneeChip({
  metadata,
}: {
  metadata?: Record<string, unknown> | null;
}) {
  const name = str(metadata, "bitrix_responsible_name");
  if (!name) return null;
  const position = str(metadata, "bitrix_responsible_position");
  const email = str(metadata, "bitrix_responsible_email");
  const title = [name, position].filter(Boolean).join(" · ") + (email ? ` <${email}>` : "") + " — from Bitrix";

  return (
    <span
      title={title}
      className="inline-flex max-w-full items-center gap-1.5 rounded-md bg-muted px-2 py-0.5 text-xs text-muted-foreground"
    >
      <span aria-hidden>👤</span>
      <span className="truncate font-medium text-foreground">{name}</span>
      {position ? <span className="truncate opacity-70">· {position}</span> : null}
      <span className="shrink-0 opacity-60">(Bitrix)</span>
    </span>
  );
}
