"use client";

import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@agora/core";
import { issueDetailOptions, issueTimelineOptions, qaEvidenceOptions } from "@agora/core/issues/queries";
import { figmaRefsFrom } from "@agora/core/figma";
import { extractDesignProposals, latestDesignAudit } from "@agora/core/design";
import { useT } from "../../i18n";
import { FigmaLinksSection } from "./figma-links-section";
import { DesignProposalSection } from "./design-proposal-section";
import { DesignAuditSection } from "./design-audit-section";
import { DesignScreenshotCompare } from "./design-screenshot-compare";
import { QADesignCompare } from "../../qa/components/qa-design-compare";
import { DesignContextReviewSection } from "./design-context-review-section";

// The Design lens — a visual workbench, matching the QA/Dev lenses' wide
// two-column shape (docs/design-stage-research.md §4, Phase 2). PRIMARY
// (left, 1fr): the Figma-reference-vs-built screenshot compare — the
// human-reviewed channel the design-compare check's DOM assertions can't
// fully replace (composed-visual issues like overlap/z-order). RIGHT
// (~380px): the existing design sections — proposal, audit, the
// verdict/mismatch/lint recap (QADesignCompare, now image-capable too), and
// a compact Figma-links list. Every section keeps its own self-gating
// (renders null when not relevant to this issue); this lens only decides
// whether the STAGE has any design signal at all, so it can show a single
// empty state instead of a blank content pane.

export function DesignLensBody({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceId();
  const { t } = useT("issues");

  const { data: issue, isLoading } = useQuery(issueDetailOptions(wsId, issueId));
  const { data: timeline = [] } = useQuery(issueTimelineOptions(issueId));
  const { data: evidence } = useQuery(qaEvidenceOptions(issueId));

  const comments = timeline
    .filter((e) => e.type === "comment")
    .map((e) => ({
      id: e.id,
      author_type: e.actor_type,
      author_id: e.actor_id,
      content: e.content ?? "",
      created_at: e.created_at,
    }));

  const figmaRefs = figmaRefsFrom(issue?.description ?? "");
  const versions = extractDesignProposals(comments);
  const audit = latestDesignAudit(comments);
  const designResult = evidence?.result?.design ?? null;
  const labelNames = new Set((issue?.labels ?? []).map((l) => l.name));
  const hasContextProposal = comments.some((comment) => comment.content.includes("<!-- design-context-proposal -->"));

  const hasSignals =
    figmaRefs.length > 0 ||
    versions.length > 0 ||
    labelNames.has("design:proposed") ||
    labelNames.has("design:approved") ||
    labelNames.has("design:changes_requested") ||
    audit !== null ||
    hasContextProposal ||
    designResult !== null;

  if (isLoading || !issue) {
    return (
      <div className="w-full px-8 py-8">
        <p className="text-sm text-muted-foreground">{t(($) => $.timeline.loading)}</p>
      </div>
    );
  }

  return (
    <div className="flex-1 overflow-y-auto">
      <div className="w-full px-8 py-8">
        {hasSignals ? (
          <div className="lg:grid lg:grid-cols-[minmax(0,1fr)_380px] lg:items-start lg:gap-6">
            <div className="order-2 min-w-0 lg:order-1">
              <DesignScreenshotCompare issueId={issueId} />
            </div>
            <div className="order-1 mb-6 lg:order-2 lg:mb-0 [&>*+*]:mt-6 [&>*+*]:border-t [&>*+*]:pt-6">
              {issue.project_id ? <DesignContextReviewSection projectId={issue.project_id} /> : null}
              <DesignProposalSection issueId={issueId} />
              <DesignAuditSection issueId={issueId} />
              <QADesignCompare design={designResult} issueId={issueId} visual="full" />
              <FigmaLinksSection issueId={issueId} description={issue.description} activeLens="design" />
            </div>
          </div>
        ) : (
          <div className="rounded-lg border border-dashed bg-muted/20 px-3 py-5 text-center">
            <p className="text-[12px] text-muted-foreground">{t(($) => $.design_lens.empty)}</p>
          </div>
        )}
      </div>
    </div>
  );
}
