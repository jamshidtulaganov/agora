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
import { QADesignCompare } from "../../qa/components/qa-design-compare";

// The Design lens — a thin re-mount of the design sections that already live
// in issue-detail's sidebar (docs/sdlc-stage-cockpit-plan.md, phase E). Every
// section keeps its own self-gating (renders null when not relevant to this
// issue); this lens only decides whether the STAGE has any design signal at
// all, so it can show a single empty state instead of a blank content pane.
// The `[&>*+*]` wrapper draws the skin-contract separator (`my-8 border-t`)
// only between sections that actually rendered something — a section that
// self-gates to null contributes no DOM node, so it never produces a stray
// hairline.

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

  const hasSignals =
    figmaRefs.length > 0 ||
    versions.length > 0 ||
    labelNames.has("design:proposed") ||
    labelNames.has("design:approved") ||
    labelNames.has("design:changes_requested") ||
    audit !== null ||
    designResult !== null;

  if (isLoading || !issue) {
    return (
      <div className="mx-auto w-full max-w-4xl px-8 py-8">
        <p className="text-sm text-muted-foreground">{t(($) => $.timeline.loading)}</p>
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-4xl px-8 py-8">
      {hasSignals ? (
        <div className="[&>*+*]:mt-8 [&>*+*]:border-t [&>*+*]:pt-8">
          <FigmaLinksSection description={issue.description} />
          <DesignProposalSection issueId={issueId} />
          <DesignAuditSection issueId={issueId} />
          <QADesignCompare design={designResult} />
        </div>
      ) : (
        <div className="rounded-lg border border-dashed bg-muted/20 px-3 py-5 text-center">
          <p className="text-[12px] text-muted-foreground">{t(($) => $.design_lens.empty)}</p>
        </div>
      )}
    </div>
  );
}
