"use client";

import { BookOpen } from "lucide-react";
import { knowledgeViewerHref } from "@agora/core/knowledge";
import { paths, useWorkspaceSlug } from "@agora/core/paths";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";
import { knowledgeCiteLabel, type KnowledgeCite } from "../lib/knowledge-cites";

const CHIP_CLASS =
  "not-prose mx-0.5 inline-flex max-w-[18rem] items-center gap-1 rounded-md border bg-muted/40 px-1.5 py-px align-baseline text-[11px] font-medium leading-4 text-muted-foreground no-underline transition-colors hover:bg-accent hover:text-foreground";

/**
 * Inline source chip for a knowledge-base citation ("Collections SOP · p. 4").
 * Opens the Knowledge viewer at the cited section. Outside a workspace route
 * there is no page to open, so it stays a plain label.
 */
export function KnowledgeCiteChip({ cite }: { cite: KnowledgeCite }) {
  const { t } = useT("assistant");
  const slug = useWorkspaceSlug();
  const label = knowledgeCiteLabel(cite) || t(($) => $.citation.source);
  const title = t(($) => $.citation.open, { source: label });
  const body = (
    <>
      <BookOpen className="size-3 shrink-0" aria-hidden />
      <span className="truncate">{label}</span>
    </>
  );

  if (!slug) {
    return (
      <span className={CHIP_CLASS} title={label}>
        {body}
      </span>
    );
  }

  const href = knowledgeViewerHref(paths.workspace(slug).knowledge(), cite.docId, cite.section);
  return (
    <AppLink href={href} className={CHIP_CLASS} title={title} aria-label={title}>
      {body}
    </AppLink>
  );
}
