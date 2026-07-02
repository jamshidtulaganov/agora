"use client";

import { useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, Palette, AlertTriangle } from "lucide-react";
import { toast } from "sonner";
import { api } from "@agora/core/api";
import { issueKeys, issueDetailOptions, issueTimelineOptions } from "@agora/core/issues/queries";
import { useWorkspaceId } from "@agora/core/hooks";
import { figmaRefsFrom } from "@agora/core/figma";
import { extractDesignProposals } from "@agora/core/design";
import type { Attachment } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import { DesignReviewDialog, type DesignProposalVersion } from "./design-review-dialog";
import { useT } from "../../i18n";

// Sidebar section for the design stage: when an issue references a Figma design
// (or already carries a design proposal / design-state label), show the current
// state, a summary of the latest proposal, and the entry point to fire a
// proposal or open the full-screen review. Extraction is client-side (the
// server persists only the label + notification in Phase 2), so this works
// against any server version.
export function DesignProposalSection({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();

  const { data: issue = null } = useQuery(issueDetailOptions(wsId, issueId));
  const { data: timeline = [] } = useQuery(issueTimelineOptions(issueId));

  const [firing, setFiring] = useState(false);
  const [reviewOpen, setReviewOpen] = useState(false);

  const figmaRefs = useMemo(() => figmaRefsFrom(issue?.description ?? ""), [issue?.description]);

  // Map comment timeline entries into the shape the extractor reads (it keys on
  // author_type/author_id; timeline entries carry actor_type/actor_id), and
  // keep each entry's attachments so the review dialog can resolve screen
  // renders by filename.
  const { versions, latestState } = useMemo(() => {
    const comments = timeline
      .filter((e) => e.type === "comment")
      .map((e) => ({
        id: e.id,
        author_type: e.actor_type,
        author_id: e.actor_id,
        content: e.content ?? "",
        created_at: e.created_at,
      }));
    const attachmentsByComment = new Map<string, Attachment[]>();
    for (const e of timeline) {
      if (e.type === "comment" && e.attachments) attachmentsByComment.set(e.id, e.attachments);
    }
    const parsed = extractDesignProposals(comments);
    const vers: DesignProposalVersion[] = parsed.map((p) => ({
      parsed: p,
      attachments: attachmentsByComment.get(p.commentId) ?? [],
    }));
    return { versions: vers, latestState: parsed.length ? parsed[parsed.length - 1]!.state : null };
  }, [timeline]);

  const labelNames = useMemo(() => new Set((issue?.labels ?? []).map((l) => l.name)), [issue?.labels]);

  // Mount only when this issue is design-relevant: it links a Figma design, has
  // a proposal, or carries a design-state label.
  const relevant =
    figmaRefs.length > 0 ||
    versions.length > 0 ||
    labelNames.has("design:proposed") ||
    labelNames.has("design:approved") ||
    labelNames.has("design:changes_requested");
  if (!relevant) return null;

  const fire = async () => {
    if (firing) return;
    setFiring(true);
    try {
      await api.sliceAction(issueId, { kind: "design_proposal" });
      void qc.invalidateQueries({ queryKey: issueKeys.tasks(issueId) });
      void qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
      toast.success(t(($) => $.design_proposal.toast_fired));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.design_proposal.toast_fire_failed));
    } finally {
      setFiring(false);
    }
  };

  const stateBadge = labelNames.has("design:approved")
    ? { label: t(($) => $.design_proposal.status_approved), cls: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400" }
    : labelNames.has("design:changes_requested")
      ? { label: t(($) => $.design_proposal.status_changes_requested), cls: "bg-amber-500/15 text-amber-600 dark:text-amber-400" }
      : latestState === "ok" || labelNames.has("design:proposed")
        ? { label: t(($) => $.design_proposal.status_proposed), cls: "bg-violet-500/15 text-violet-600 dark:text-violet-400" }
        : null;

  const latest = versions.length ? versions[versions.length - 1] : null;
  const latestProposal = latest?.parsed.proposal ?? null;
  const counts = countVerdicts(latestProposal);

  return (
    <div>
      <div className="mb-2 flex items-center gap-1 px-2 py-1 text-xs font-medium">
        <Palette className="!size-3 shrink-0 text-muted-foreground" />
        {t(($) => $.design_proposal.title)}
        {stateBadge && (
          <span className={`ml-auto shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${stateBadge.cls}`}>
            {stateBadge.label}
          </span>
        )}
      </div>

      <div className="space-y-2 pl-2">
        {/* Newest block failed to parse — surface it explicitly, never hide. */}
        {latestState === "invalid" && (
          <div className="flex items-start gap-1.5 rounded-md bg-destructive/10 px-2.5 py-2 text-xs text-destructive">
            <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
            <span>{t(($) => $.design_proposal.invalid_hint)}</span>
          </div>
        )}

        {latestState === "blocked" && (
          <div className="rounded-md bg-amber-500/10 px-2.5 py-2 text-xs text-amber-700 dark:text-amber-400">
            {t(($) => $.design_proposal.blocked_hint)}
            {latestProposal?.reason ? ` (${latestProposal.reason})` : ""}
          </div>
        )}

        {latestState === "ok" && latestProposal && (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.design_proposal.summary_line, {
              screens: latestProposal.screens.length,
              reuse: counts.reuse,
              new: counts.new,
            })}
          </p>
        )}

        <div className="flex flex-wrap gap-1.5">
          {latestState === "ok" && (
            <Button variant="outline" size="sm" onClick={() => setReviewOpen(true)}>
              {t(($) => $.design_proposal.open_review)}
            </Button>
          )}
          <Button variant="outline" size="sm" onClick={() => void fire()} disabled={firing}>
            {firing ? <Loader2 className="size-3.5 animate-spin" /> : null}
            {versions.length > 0
              ? t(($) => $.design_proposal.refire)
              : t(($) => $.design_proposal.fire)}
          </Button>
        </div>
      </div>

      {reviewOpen && versions.length > 0 && (
        <DesignReviewDialog issueId={issueId} versions={versions} onClose={() => setReviewOpen(false)} />
      )}
    </div>
  );
}

function countVerdicts(p: { components: { verdict: "reuse" | "extend" | "new" }[] } | null): {
  reuse: number;
  extend: number;
  new: number;
} {
  const c = { reuse: 0, extend: 0, new: 0 };
  if (!p) return c;
  for (const comp of p.components) c[comp.verdict]++;
  return c;
}
