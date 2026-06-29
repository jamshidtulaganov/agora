/* eslint-disable i18next/no-literal-string */
"use client";

import { ExternalLink } from "lucide-react";

// BitrixTaskLink renders a deep link back to the original task in the Bitrix
// portal, read from the issue's bitrix_task_url metadata (stamped by the import
// sync). Renders nothing for issues that didn't come from Bitrix. Bitrix-
// specific copy is intentionally not translated, matching the rest of the
// bitrix slice.

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
  return (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      title={stage ? `Bitrix stage: ${stage}` : "Open the original task in Bitrix"}
      className="inline-flex max-w-full items-center gap-1.5 rounded-md bg-muted px-2 py-0.5 text-xs text-muted-foreground transition-colors hover:text-foreground"
    >
      <ExternalLink className="size-3 shrink-0" />
      <span className="truncate">Open in Bitrix</span>
      {stage ? <span className="shrink-0 opacity-70">· {stage}</span> : null}
    </a>
  );
}
