"use client";

import { BarChart3, Code2, FileText, Table2 } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useT } from "../../i18n";
import { toArtifactKind } from "../lib/artifact";

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
