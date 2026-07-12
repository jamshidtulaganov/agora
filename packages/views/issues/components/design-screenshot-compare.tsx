"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { issueTimelineOptions } from "@agora/core/issues/queries";
import { latestQAResultScreenshots, pairDesignScreenshots } from "@agora/core/design";
import { useT } from "../../i18n";
import { DesignScreenshotGrid } from "../../qa/components/qa-design-compare";

// The design lens's primary visual pane: Figma reference vs built screen,
// side by side, per screen the design-compare check screenshotted
// (docs/design-stage-research.md §4). Sources images the same way
// QADesignCompare does (packages/core/design/screenshots.ts) — both mount
// the same DesignScreenshotGrid so the pairing + lightbox logic lives in one
// place; this component only owns the primary-pane chrome (size, empty
// state). The underlying `issueTimelineOptions(issueId)` query is shared
// with the rest of the lens via the TanStack Query cache — mounting this
// alongside DesignLensBody's own timeline fetch does not double the network
// call.
export function DesignScreenshotCompare({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const { data: timeline = [] } = useQuery(issueTimelineOptions(issueId));

  const pairs = useMemo(() => {
    const comments = timeline
      .filter((e) => e.type === "comment")
      .map((e) => ({ author_type: e.actor_type, content: e.content ?? "", attachments: e.attachments }));
    return pairDesignScreenshots(latestQAResultScreenshots(comments));
  }, [timeline]);

  if (pairs.length === 0) {
    return (
      <div className="flex h-full min-h-[280px] flex-col items-center justify-center rounded-lg border border-dashed bg-muted/20 px-3 py-10 text-center">
        <p className="text-[12px] text-muted-foreground">{t(($) => $.design_lens.screenshots_empty)}</p>
      </div>
    );
  }

  return <DesignScreenshotGrid pairs={pairs} size="lg" />;
}
