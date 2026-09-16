"use client";

import { BarChart3, Code2, FileText, Table2 } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { Badge } from "@agora/ui/components/ui/badge";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import type { ArtifactToolRef } from "../lib/artifact";
import { toArtifactKind } from "../lib/artifact";

interface ArtifactCardProps {
  artifact: ArtifactToolRef;
  /** Absent in surfaces with nowhere to open the pane — the card goes static. */
  onOpen?: () => void;
}

const KIND_ICONS: Record<string, LucideIcon> = {
  chart: BarChart3,
  table: Table2,
  markdown: FileText,
  html: Code2,
};

/** Icon for a wire kind; an unknown future kind falls back to the doc glyph. */
export function artifactKindIcon(kind: string): LucideIcon {
  const known = toArtifactKind(kind);
  return (known && KIND_ICONS[known]) ?? FileText;
}

/** Human label for a wire kind. Unknown kinds get a generic label rather than
 *  the raw enum string (enum drift downgrades, never leaks). */
export function useArtifactKindLabel(): (kind: string) => string {
  const { t } = useT("assistant");
  return (kind: string) => {
    switch (toArtifactKind(kind)) {
      case "chart":
        return t(($) => $.artifact.kind.chart);
      case "table":
        return t(($) => $.artifact.kind.table);
      case "markdown":
        return t(($) => $.artifact.kind.markdown);
      case "html":
        return t(($) => $.artifact.kind.html);
      default:
        return t(($) => $.artifact.kind.unknown);
    }
  };
}

/**
 * Transcript row for a create_artifact / update_artifact tool result — the
 * one tool whose output is a *thing* the user reopens rather than an action
 * they glance at, so it gets a card where every other tool gets a chip. Sits
 * at the same `ml-8` indent as ToolChip so the action column stays straight.
 *
 * An unrecognised tool_result never reaches here: message-list falls back to
 * the plain chip when `parseArtifactToolResult` returns null.
 */
export function ArtifactCard({ artifact, onOpen }: ArtifactCardProps) {
  const { t } = useT("assistant");
  const Icon = artifactKindIcon(artifact.kind);
  const title = artifact.title.trim() || t(($) => $.artifact.untitled);

  const className =
    "ml-8 flex w-full max-w-sm items-center gap-2.5 rounded-lg border border-border bg-card px-3 py-2 text-left text-sm";

  const body = (
    <>
      <Icon className="size-4 shrink-0 text-muted-foreground" />
      <span className="min-w-0 flex-1 truncate font-medium">{title}</span>
      {artifact.version > 1 && (
        <Badge variant="secondary" className="shrink-0 tabular-nums">
          {t(($) => $.artifact.version_badge, { version: artifact.version })}
        </Badge>
      )}
    </>
  );

  if (!onOpen) {
    return <div className={className}>{body}</div>;
  }

  return (
    <button
      type="button"
      onClick={onOpen}
      aria-label={t(($) => $.artifact.open_aria, { title })}
      className={cn(
        className,
        "cursor-pointer transition-colors hover:border-brand/50 hover:bg-accent/40 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
      )}
    >
      {body}
    </button>
  );
}
