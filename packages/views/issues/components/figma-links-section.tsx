"use client";

import { Palette, ExternalLink, ArrowUpRight } from "lucide-react";
import { figmaRefsFrom } from "@agora/core/figma";
import { useWorkspacePaths } from "@agora/core/paths";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";

// Sidebar section listing the Figma designs an issue references. Extraction is
// client-side (shared TS twin of the server primitive), so the section works
// against any server version — no dedicated endpoint, no schema drift risk.
//
// Also the entry point into the design lens: design is no longer an SDLC
// stepper stage the user clicks through (it's a dev-build INPUT — see
// packages/core/issues/stage.ts) so `?lens=design` needs a reachable link
// somewhere. This component renders in two places (issue-detail.tsx's
// sidebar AND inside the design lens's own right column, design-lens.tsx),
// so `activeLens` lets the caller suppress the link when we're already
// inside the view it points to.
//
// The `?lens=design` query string is built as a literal here (not imported
// from lens.ts's LENS_QUERY_KEY/registry) to avoid a module cycle: lens.ts
// imports DesignLensBody (design-lens.tsx), which imports this component.
export function FigmaLinksSection({
  issueId,
  description,
  activeLens,
}: {
  issueId: string;
  description: string | null | undefined;
  /** The lens currently mounted on the issue-detail page. Omit when the
   *  caller has no lens context (the link always shows). */
  activeLens?: string;
}) {
  const { t } = useT("issues");
  const paths = useWorkspacePaths();
  const refs = figmaRefsFrom(description ?? "");
  if (refs.length === 0) return null;
  const showDesignViewLink = activeLens !== "design";
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
        {showDesignViewLink && (
          <AppLink
            href={`${paths.issueDetail(issueId)}?lens=design`}
            className="group -mx-2 flex items-center gap-1.5 rounded-md px-2 py-1.5 text-xs text-muted-foreground transition-colors hover:bg-accent/50 hover:text-foreground"
          >
            <span className="truncate">{t(($) => $.figma_links.open_design_view)}</span>
            <ArrowUpRight className="!size-3 shrink-0 opacity-0 transition-opacity group-hover:opacity-100" />
          </AppLink>
        )}
      </div>
    </div>
  );
}
