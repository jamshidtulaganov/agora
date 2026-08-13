/* eslint-disable i18next/no-literal-string */
"use client";

import { AlertTriangle, ExternalLink } from "lucide-react";

// BitrixTaskLink renders a deep link back to the original task in the Bitrix
// portal, read from the issue's bitrix_task_url metadata (stamped by the import
// sync). Renders nothing for issues that didn't come from Bitrix. Bitrix-
// specific copy is intentionally not translated, matching the rest of the
// bitrix slice.
//
// The Стадия (kanban column) is the PRIMARY label, not a suffix: it is what
// decides the Agora status, so "which column is this task actually in" is the
// question this chip exists to answer. The generic "Open in Bitrix" wording only
// appears when no stage is known — previously it ate the width and truncated the
// stage away in a narrow properties panel.

function str(meta: Record<string, unknown> | null | undefined, key: string): string {
  const v = meta?.[key];
  return typeof v === "string" ? v.trim() : "";
}

export function BitrixTaskLink({
  metadata,
}: {
  metadata?: Record<string, unknown> | null;
}) {
  const url = str(metadata, "bitrix_task_url");
  if (!url) return null;
  const stage = str(metadata, "bitrix_stage");
  // Explicit "no" only — a missing key means an older mirror that predates the
  // flag, which must not render as a warning.
  const unmapped = stage !== "" && str(metadata, "bitrix_stage_mapped") === "no";
  const title = stage
    ? unmapped
      ? `Bitrix stage: ${stage} — not mapped to an Agora status, so the status below came from the coarse Bitrix STATUS`
      : `Bitrix stage: ${stage}`
    : "Open the original task in Bitrix";
  return (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      title={title}
      className="inline-flex max-w-full items-center gap-1.5 rounded-md bg-muted px-2 py-0.5 text-xs text-muted-foreground transition-colors hover:text-foreground"
    >
      <ExternalLink className="size-3 shrink-0" />
      <span className="truncate">{stage || "Open in Bitrix"}</span>
      {unmapped ? <AlertTriangle className="size-3 shrink-0 text-warning" /> : null}
    </a>
  );
}
