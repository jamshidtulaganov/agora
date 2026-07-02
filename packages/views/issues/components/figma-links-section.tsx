"use client";

import { Palette, ExternalLink } from "lucide-react";
import { figmaRefsFrom } from "@agora/core/figma";
import { useT } from "../../i18n";

// Sidebar section listing the Figma designs an issue references. Extraction is
// client-side (shared TS twin of the server primitive), so the section works
// against any server version — no dedicated endpoint, no schema drift risk.
export function FigmaLinksSection({ description }: { description: string | null | undefined }) {
  const { t } = useT("issues");
  const refs = figmaRefsFrom(description ?? "");
  if (refs.length === 0) return null;
  return (
    <div>
      <div className="mb-2 flex w-full items-center gap-1 px-2 py-1 text-xs font-medium">
        <Palette className="!size-3 shrink-0 text-muted-foreground" />
        {refs.length === 1
          ? t(($) => $.figma_links.chip_label)
          : t(($) => $.figma_links.chip_label_n, { count: refs.length })}
      </div>
      <div className="space-y-0.5 pl-2">
        {refs.map((ref) => (
          <a
            key={`${ref.file_key}|${ref.node_id}`}
            href={ref.url}
            target="_blank"
            rel="noreferrer"
            className="group -mx-2 flex items-center gap-1.5 rounded-md px-2 py-1.5 text-xs text-muted-foreground transition-colors hover:bg-accent/50 hover:text-foreground"
            title={t(($) => $.figma_links.open_in_figma)}
          >
            <span className="truncate font-mono">
              {ref.file_key.slice(0, 8)}…{ref.node_id ? ` · ${ref.node_id}` : ""}
            </span>
            <ExternalLink className="!size-3 shrink-0 opacity-0 transition-opacity group-hover:opacity-100" />
          </a>
        ))}
      </div>
    </div>
  );
}
