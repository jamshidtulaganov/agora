/* eslint-disable i18next/no-literal-string -- design review dialog; i18n follow-up */
"use client";

import { useMemo, useState, type KeyboardEvent, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  Blocks,
  ChevronLeft,
  ChevronRight,
  ExternalLink,
  Images,
  ListTree,
  Loader2,
  Palette,
} from "lucide-react";
import { api } from "@agora/core/api";
import { issueKeys } from "@agora/core/issues/queries";
import { useWorkspaceId } from "@agora/core/hooks";
import { useWorkspacePaths } from "@agora/core/paths";
import { resolvePublicFileUrl } from "@agora/core/workspace/avatar-url";
import type { DesignProposal, ParsedDesignProposal, DesignVerdict } from "@agora/core/design";
import type { Attachment } from "@agora/core/types";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "@agora/ui/components/ui/dialog";
import { Button } from "@agora/ui/components/ui/button";
import { Input } from "@agora/ui/components/ui/input";
import { Textarea } from "@agora/ui/components/ui/textarea";
import { Checkbox } from "@agora/ui/components/ui/checkbox";
import { useT } from "../../i18n";
import { useNavigation } from "../../navigation";
import { openExternal } from "../../platform";
import { useAttachmentPreview } from "../../editor";
import { useAuthenticatedMediaSrc } from "../../editor/hooks/use-authenticated-media-src";

// One reviewable revision: a parsed proposal block + the attachments on the
// comment that carried it (so screens[].render can resolve to an image).
export interface DesignProposalVersion {
  parsed: ParsedDesignProposal;
  attachments: Attachment[];
}

interface DesignReviewDialogProps {
  issueId: string;
  versions: DesignProposalVersion[]; // oldest → newest
  onClose: () => void;
}

interface OverrideState {
  include: boolean;
  title: string;
  description: string;
}

const VERDICT_STYLES: Record<DesignVerdict, string> = {
  reuse: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
  extend: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  new: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
};

interface FigmaEmbed {
  sourceUrl: string;
  embedUrl: string;
}

function figmaEmbedFrom(
  ref: DesignProposal["figma"][number] | undefined,
): FigmaEmbed | null {
  if (!ref?.url) return null;

  let source: URL;
  try {
    source = new URL(ref.url);
  } catch {
    return null;
  }

  if (source.protocol !== "https:" || !["figma.com", "www.figma.com"].includes(source.hostname)) {
    return null;
  }

  const match = source.pathname.match(
    /^\/(design|file|proto|board|slides|deck)\/([A-Za-z0-9]{10,})(\/[^?#]*)?/,
  );
  if (!match) return null;

  const kind = match[1] === "file" ? "design" : match[1];
  const fileKey = match[2];
  const fileName = match[3] ?? "";
  const embed = new URL(`https://embed.figma.com/${kind}/${fileKey}${fileName}`);
  const nodeId = source.searchParams.get("node-id") || ref.node_id.replaceAll(":", "-");
  if (nodeId) embed.searchParams.set("node-id", nodeId);
  embed.searchParams.set("embed-host", "agora");
  embed.searchParams.set("theme", "system");

  return { sourceUrl: source.toString(), embedUrl: embed.toString() };
}

export function DesignReviewDialog({ issueId, versions, onClose }: DesignReviewDialogProps) {
  const { t } = useT("issues");
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const workspacePaths = useWorkspacePaths();
  const navigation = useNavigation();
  const attachmentPreview = useAttachmentPreview();

  // Default to the newest revision.
  const [versionIdx, setVersionIdx] = useState(versions.length - 1);
  const version = versions[versionIdx];
  const proposal = version?.parsed.proposal ?? null;

  const [note, setNote] = useState("");
  const [submitting, setSubmitting] = useState<"approve" | "request_changes" | null>(null);
  const [confirmApprove, setConfirmApprove] = useState(false);
  const [supersedePrompt, setSupersedePrompt] = useState(false);

  // Per-sub-issue overrides, keyed by index; default include=true with the
  // agent's original text.
  const [overrides, setOverrides] = useState<Record<number, OverrideState>>({});
  const overrideFor = (i: number, sub: { title: string; description: string }): OverrideState =>
    overrides[i] ?? { include: true, title: sub.title, description: sub.description };
  const setOverride = (i: number, patch: Partial<OverrideState>, sub: { title: string; description: string }) =>
    setOverrides((prev) => ({ ...prev, [i]: { ...overrideFor(i, sub), ...patch } }));

  const attachmentFor = (render: string): Attachment | undefined => {
    if (!render || !version) return undefined;
    return version.attachments.find((a) => a.filename === render);
  };

  const refresh = () => {
    void qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
    void qc.invalidateQueries({ queryKey: issueKeys.detail(wsId, issueId) });
    // Approval decomposes into sub-issues — refresh the parent's children panel.
    void qc.invalidateQueries({ queryKey: issueKeys.children(wsId, issueId) });
  };

  const submit = async (action: "approve" | "request_changes", supersede = false) => {
    if (submitting) return;
    if (action === "request_changes" && !note.trim()) {
      toast.error(t(($) => $.design_proposal.changes_note_required));
      return;
    }
    setSubmitting(action);
    try {
      const sub_issue_overrides =
        action === "approve" && proposal
          ? proposal.sub_issues.map((sub, i) => {
              const o = overrideFor(i, sub);
              return {
                index: i,
                include: o.include,
                ...(o.title !== sub.title ? { title: o.title } : {}),
                ...(o.description !== sub.description ? { description: o.description } : {}),
              };
            })
          : undefined;
      await api.createDesignReview(issueId, {
        action,
        note: note.trim() || undefined,
        ...(supersede ? { supersede_previous: true } : {}),
        ...(sub_issue_overrides ? { sub_issue_overrides } : {}),
      });
      refresh();
      toast.success(
        action === "approve"
          ? t(($) => $.design_proposal.toast_approved)
          : t(($) => $.design_proposal.toast_changes_requested),
      );
      onClose();
    } catch (e) {
      const msg = e instanceof Error ? e.message : "";
      if (msg.includes("already_decomposed")) {
        toast.message(t(($) => $.design_proposal.already_decomposed));
        refresh();
        onClose();
      } else if (msg.includes("previous_decomposition_exists")) {
        // Offer to supersede the prior batch.
        setSupersedePrompt(true);
      } else {
        toast.error(msg || t(($) => $.design_proposal.toast_review_failed));
      }
    } finally {
      setSubmitting(null);
    }
  };

  const counts = useMemo(() => countVerdicts(proposal), [proposal]);
  const figmaEmbed = useMemo(
    () => proposal?.figma.map((ref) => figmaEmbedFrom(ref)).find(Boolean) ?? null,
    [proposal],
  );
  const isTokenModeClient = Boolean((api.getBaseUrl?.() ?? "").replace(/\/+$/, ""));
  const reviewItemCount = proposal
    ? proposal.screens.length
      + proposal.components.length
      + proposal.deviations.length
      + proposal.sub_issues.length
      + proposal.open_questions.length
    : 0;
  const hasReviewContent = reviewItemCount > 0;
  const canApprove = Boolean(
    proposal
      && hasReviewContent
      && version?.parsed.state !== "blocked",
  );
  const configureFigma = () => {
    onClose();
    navigation.push(`${workspacePaths.settings()}?tab=integrations&integration=figma`);
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="flex max-h-[calc(100dvh-1.5rem)] w-[calc(100vw-1.5rem)] max-w-none flex-col gap-0 overflow-hidden p-0 sm:max-h-[min(92dvh,920px)] sm:w-[min(94vw,1180px)] sm:max-w-[1180px]">
        {/* Header */}
        <div className="shrink-0 border-b border-border bg-muted/20 px-5 py-4 pr-14 sm:px-6 sm:py-5 sm:pr-14">
          <div className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-start sm:justify-between sm:gap-6">
            <div className="flex min-w-0 items-start gap-3">
              <span className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-border bg-background text-violet-500 shadow-xs">
                <Palette className="size-4" aria-hidden="true" />
              </span>
              <div className="min-w-0">
                <DialogTitle className="text-base font-semibold leading-6 text-pretty">
                  {t(($) => $.design_proposal.review_title)}
                </DialogTitle>
                <DialogDescription className="mt-0.5 max-w-xl text-xs leading-5 text-pretty">
                  {t(($) => $.design_proposal.review_description)}
                </DialogDescription>
              </div>
            </div>

            <div className="shrink-0">
              {versions.length > 1 ? (
                <select
                  className="h-9 rounded-lg border border-border bg-background px-3 text-xs text-foreground shadow-xs outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
                  value={versionIdx}
                  onChange={(e) => setVersionIdx(Number(e.target.value))}
                  aria-label={t(($) => $.design_proposal.revision)}
                >
                  {versions.map((_, i) => (
                    <option key={i} value={i}>
                      {t(($) => $.design_proposal.revision)} v{i + 1}
                      {i === versions.length - 1 ? " ✓" : ""}
                    </option>
                  ))}
                </select>
              ) : (
                <span className="inline-flex h-8 items-center rounded-full border border-border bg-background px-3 text-xs font-medium text-muted-foreground shadow-xs">
                  {t(($) => $.design_proposal.revision)} v1
                </span>
              )}
            </div>
          </div>
        </div>

        <div className="min-h-0 flex-1 space-y-5 overflow-y-auto overscroll-contain px-5 py-5 sm:px-6">
          {!proposal ? (
            <div className="rounded-xl border border-dashed border-border bg-muted/20 px-5 py-8 text-center">
              <Palette className="mx-auto size-5 text-muted-foreground" aria-hidden="true" />
              <h3 className="mt-3 text-sm font-semibold">{t(($) => $.design_proposal.invalid_title)}</h3>
              <p className="mx-auto mt-1 max-w-md text-xs leading-5 text-muted-foreground text-pretty">
                {t(($) => $.design_proposal.invalid_hint)}
              </p>
            </div>
          ) : (
            <>
              {version?.parsed.state === "blocked" && (
                <div className="flex flex-wrap items-center justify-between gap-3 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">
                  <div>
                    <p>
                      {t(($) => $.design_proposal.status_blocked)}
                      {proposal.reason ? ` — ${proposal.reason}` : ""}
                    </p>
                    {proposal.reason === "credential_missing" && (
                      <p className="mt-0.5 text-destructive/80">
                        {t(($) => $.design_proposal.credential_missing_detail)}
                      </p>
                    )}
                  </div>
                  {proposal.reason === "credential_missing" && (
                    <Button type="button" size="sm" variant="outline" onClick={configureFigma}>
                      {t(($) => $.design_proposal.configure_figma)}
                    </Button>
                  )}
                </div>
              )}

              {version?.parsed.state === "ok" && !hasReviewContent && (
                <div className="rounded-xl border border-dashed border-border bg-muted/20 px-5 py-8 text-center">
                  <Palette className="mx-auto size-5 text-muted-foreground" aria-hidden="true" />
                  <h3 className="mt-3 text-sm font-semibold">
                    {t(($) => $.design_proposal.empty_title)}
                  </h3>
                  <p className="mx-auto mt-1 max-w-md text-xs leading-5 text-muted-foreground text-pretty">
                    {t(($) => $.design_proposal.empty_body)}
                  </p>
                </div>
              )}

              {figmaEmbed && (
                <section className="overflow-hidden rounded-xl border border-border bg-background shadow-xs">
                  <div className="flex min-w-0 items-center justify-between gap-4 border-b border-border bg-muted/20 px-4 py-3">
                    <div className="min-w-0">
                      <h3 className="text-xs font-semibold">
                        {t(($) => $.design_proposal.figma_preview)}
                      </h3>
                      <p className="mt-0.5 truncate text-[11px] text-muted-foreground">
                        {isTokenModeClient
                          ? t(($) => $.design_proposal.figma_desktop_description)
                          : t(($) => $.design_proposal.figma_preview_description)}
                      </p>
                    </div>
                    <button
                      type="button"
                      onClick={() => openExternal(figmaEmbed.sourceUrl)}
                      className="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-md border border-border bg-background px-3 text-xs font-medium text-foreground shadow-xs outline-none transition-colors hover:bg-muted focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
                    >
                      <ExternalLink className="size-3.5" aria-hidden="true" />
                      {t(($) => $.figma_links.open_in_figma)}
                    </button>
                  </div>
                  {isTokenModeClient ? (
                    <div className="flex min-h-36 items-center justify-center px-6 py-8 text-center">
                      <div className="max-w-lg">
                        <span className="mx-auto flex size-10 items-center justify-center rounded-xl border border-border bg-muted/40 text-violet-500">
                          <Palette className="size-4" aria-hidden="true" />
                        </span>
                        <p className="mt-3 text-xs leading-5 text-muted-foreground text-pretty">
                          {t(($) => $.design_proposal.figma_desktop_notice)}
                        </p>
                      </div>
                    </div>
                  ) : (
                    <iframe
                      src={figmaEmbed.embedUrl}
                      title={t(($) => $.design_proposal.figma_embed_title)}
                      allowFullScreen
                      loading="eager"
                      referrerPolicy="strict-origin-when-cross-origin"
                      className="h-[min(52dvh,520px)] min-h-80 w-full bg-muted/10"
                    />
                  )}
                </section>
              )}

              {hasReviewContent && (
                <div
                  className="grid grid-cols-3 divide-x divide-border overflow-hidden rounded-xl border border-border bg-muted/15"
                  aria-label={t(($) => $.design_proposal.review_coverage)}
                >
                  <ReviewMetric
                    icon={<Images className="size-3.5" aria-hidden="true" />}
                    value={proposal.screens.length}
                    label={t(($) => $.design_proposal.screens)}
                  />
                  <ReviewMetric
                    icon={<Blocks className="size-3.5" aria-hidden="true" />}
                    value={proposal.components.length}
                    label={t(($) => $.design_proposal.components)}
                  />
                  <ReviewMetric
                    icon={<ListTree className="size-3.5" aria-hidden="true" />}
                    value={proposal.sub_issues.length}
                    label={t(($) => $.design_proposal.sub_issues)}
                  />
                </div>
              )}

              {/* Screens gallery */}
              {proposal.screens.length > 0 && (
                <section>
                  <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                    {t(($) => $.design_proposal.screens)} ({proposal.screens.length})
                  </h3>
                  <DesignScreensCarousel
                    key={versionIdx}
                    items={proposal.screens.map((screen) => ({
                      screen,
                      attachment: attachmentFor(screen.render),
                    }))}
                    noRenderLabel={t(($) => $.design_proposal.no_render)}
                    onOpen={(attachment) => attachmentPreview.open({ kind: "full", attachment })}
                  />
                </section>
              )}

              {/* Component verdicts */}
              {proposal.components.length > 0 && (
                <section>
                  <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                    {t(($) => $.design_proposal.components)} · {counts.reuse} {t(($) => $.design_proposal.verdict_reuse)} ·{" "}
                    {counts.extend} {t(($) => $.design_proposal.verdict_extend)} · {counts.new} {t(($) => $.design_proposal.verdict_new)}
                  </h3>
                  <ul className="divide-y divide-border rounded-md border border-border">
                    {proposal.components.map((c, i) => (
                      <li key={i} className="flex items-start gap-2 px-3 py-2 text-xs">
                        <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${VERDICT_STYLES[c.verdict]}`}>
                          {t(($) => $.design_proposal[`verdict_${c.verdict}`])}
                        </span>
                        <div className="min-w-0 flex-1">
                          <div className="font-medium">{c.name}</div>
                          {c.code_ref && <code className="text-[11px] text-muted-foreground">{c.code_ref}</code>}
                          {c.notes && <p className="text-[11px] text-muted-foreground">{c.notes}</p>}
                        </div>
                      </li>
                    ))}
                  </ul>
                </section>
              )}

              {/* Deviations — questions for the human */}
              {proposal.deviations.length > 0 && (
                <section>
                  <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                    {t(($) => $.design_proposal.deviations)}
                  </h3>
                  <ul className="space-y-1.5">
                    {proposal.deviations.map((d, i) => (
                      <li key={i} className="rounded-md bg-amber-500/10 px-3 py-2 text-xs">
                        <span className="font-medium">{d.question || d.aspect}</span>
                        {(d.figma_value || d.project_value) && (
                          <span className="text-muted-foreground">
                            {" "}
                            — {d.figma_value} → {d.project_value}
                          </span>
                        )}
                      </li>
                    ))}
                  </ul>
                </section>
              )}

              {/* Sub-issue plan */}
              {proposal.sub_issues.length > 0 && (
                <section>
                  <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                    {t(($) => $.design_proposal.sub_issues)} ({proposal.sub_issues.length})
                  </h3>
                  <ul className="space-y-2">
                    {proposal.sub_issues.map((sub, i) => {
                      const o = overrideFor(i, sub);
                      return (
                        <li key={i} className="rounded-md border border-border px-3 py-2">
                          <div className="flex items-center gap-2">
                            <Checkbox
                              checked={o.include}
                              onCheckedChange={(v) => setOverride(i, { include: v === true }, sub)}
                              aria-label={t(($) => $.design_proposal.include_sub_issue)}
                            />
                            <Input
                              value={o.title}
                              onChange={(e) => setOverride(i, { title: e.target.value }, sub)}
                              disabled={!o.include}
                              className="h-7 flex-1 text-xs"
                              aria-label={t(($) => $.design_proposal.edit_title)}
                            />
                            {sub.depends_on.length > 0 && (
                              <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                                {t(($) => $.design_proposal.waits_on)} {sub.depends_on.map((d) => `#${d + 1}`).join(", ")}
                              </span>
                            )}
                          </div>
                          {o.include && (
                            <Textarea
                              value={o.description}
                              onChange={(e) => setOverride(i, { description: e.target.value }, sub)}
                              className="mt-1.5 min-h-[3rem] text-xs"
                              aria-label={t(($) => $.design_proposal.edit_description)}
                            />
                          )}
                        </li>
                      );
                    })}
                  </ul>
                </section>
              )}

              {/* Open questions */}
              {proposal.open_questions.length > 0 && (
                <section>
                  <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                    {t(($) => $.design_proposal.open_questions)}
                  </h3>
                  <ul className="list-disc space-y-1 pl-4 text-xs text-muted-foreground">
                    {proposal.open_questions.map((q, i) => (
                      <li key={i}>{q}</li>
                    ))}
                  </ul>
                </section>
              )}

            </>
          )}
        </div>
        {attachmentPreview.modal}

        {/* Triage bar */}
        <div className="shrink-0 space-y-3 border-t border-border bg-muted/20 px-5 py-4 sm:px-6">
          <div className="flex items-baseline justify-between gap-3">
            <label htmlFor="design-review-note" className="text-xs font-medium text-foreground">
              {t(($) => $.design_proposal.review_note_label)}
            </label>
            <span id="design-review-note-hint" className="text-[11px] text-muted-foreground">
              {t(($) => $.design_proposal.review_note_hint)}
            </span>
          </div>
          <Textarea
            id="design-review-note"
            name="design-review-note"
            value={note}
            onChange={(e) => setNote(e.target.value)}
            placeholder={t(($) => $.design_proposal.changes_note_placeholder)}
            autoComplete="off"
            aria-describedby="design-review-note-hint"
            className="max-h-32 min-h-16 resize-y bg-background text-xs"
          />
          {supersedePrompt ? (
            <div className="flex items-center justify-between gap-2">
              <span className="text-xs text-amber-700 dark:text-amber-400">
                {t(($) => $.design_proposal.previous_decomposition_body)}
              </span>
              <div className="flex gap-2">
                <Button variant="ghost" size="sm" onClick={() => setSupersedePrompt(false)} disabled={!!submitting}>
                  {t(($) => $.design_proposal.cancel)}
                </Button>
                <Button size="sm" onClick={() => void submit("approve", true)} disabled={!!submitting}>
                  {submitting === "approve" ? <Loader2 className="size-3.5 animate-spin" /> : t(($) => $.design_proposal.supersede_confirm)}
                </Button>
              </div>
            </div>
          ) : confirmApprove ? (
            <div className="flex items-center justify-between gap-2">
              <span className="text-xs text-muted-foreground">{t(($) => $.design_proposal.approve_confirm_body)}</span>
              <div className="flex gap-2">
                <Button variant="ghost" size="sm" onClick={() => setConfirmApprove(false)} disabled={!!submitting}>
                  {t(($) => $.design_proposal.cancel)}
                </Button>
                <Button size="sm" onClick={() => void submit("approve")} disabled={!!submitting}>
                  {submitting === "approve" ? <Loader2 className="size-3.5 animate-spin" /> : t(($) => $.design_proposal.approve)}
                </Button>
              </div>
            </div>
          ) : (
            <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
              <Button
                variant="outline"
                size="sm"
                onClick={() => void submit("request_changes")}
                disabled={!!submitting}
              >
                {submitting === "request_changes" ? (
                  <Loader2 className="size-3.5 animate-spin" />
                ) : (
                  t(($) => $.design_proposal.request_changes)
                )}
              </Button>
              <Button size="sm" onClick={() => setConfirmApprove(true)} disabled={!!submitting || !canApprove}>
                {t(($) => $.design_proposal.approve)}
              </Button>
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

interface DesignScreenItem {
  screen: DesignProposal["screens"][number];
  attachment: Attachment | undefined;
}

function DesignScreensCarousel({
  items,
  noRenderLabel,
  onOpen,
}: {
  items: DesignScreenItem[];
  noRenderLabel: string;
  onOpen: (attachment: Attachment) => void;
}) {
  const { t } = useT("issues");
  const [activeIndex, setActiveIndex] = useState(0);
  const active = items[activeIndex] ?? items[0];
  if (!active) return null;

  const go = (delta: number) => {
    setActiveIndex((current) => (current + delta + items.length) % items.length);
  };
  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      go(-1);
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      go(1);
    }
  };

  return (
    <div
      className="overflow-hidden rounded-xl border border-border bg-muted/10 shadow-xs"
      role="region"
      aria-label={t(($) => $.design_proposal.carousel_label)}
      tabIndex={0}
      onKeyDown={onKeyDown}
    >
      <div className="relative bg-black/95">
        <DesignScreenImage
          item={active}
          noRenderLabel={noRenderLabel}
          className="aspect-video max-h-[min(58dvh,620px)] w-full object-contain"
          onOpen={onOpen}
          openLabel={t(($) => $.design_proposal.open_screen_preview, {
            name: active.screen.name,
          })}
        />

        {items.length > 1 && (
          <>
            <button
              type="button"
              onClick={() => go(-1)}
              aria-label={t(($) => $.design_proposal.previous_screen)}
              className="absolute left-3 top-1/2 flex size-9 -translate-y-1/2 items-center justify-center rounded-full border border-white/20 bg-black/65 text-white shadow-lg backdrop-blur-sm transition-colors hover:bg-black/85 focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-white/60"
            >
              <ChevronLeft className="size-5" aria-hidden="true" />
            </button>
            <button
              type="button"
              onClick={() => go(1)}
              aria-label={t(($) => $.design_proposal.next_screen)}
              className="absolute right-3 top-1/2 flex size-9 -translate-y-1/2 items-center justify-center rounded-full border border-white/20 bg-black/65 text-white shadow-lg backdrop-blur-sm transition-colors hover:bg-black/85 focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-white/60"
            >
              <ChevronRight className="size-5" aria-hidden="true" />
            </button>
          </>
        )}

        <span
          className="absolute bottom-3 right-3 rounded-full border border-white/15 bg-black/70 px-2.5 py-1 text-[11px] font-medium tabular-nums text-white backdrop-blur-sm"
          aria-live="polite"
        >
          {t(($) => $.design_proposal.screen_position, {
            current: activeIndex + 1,
            total: items.length,
          })}
        </span>
      </div>

      <div className="border-t border-border bg-background px-4 py-3">
        <p className="text-sm font-semibold text-foreground">{active.screen.name}</p>
        {active.screen.summary && (
          <p className="mt-0.5 text-xs leading-5 text-muted-foreground">
            {active.screen.summary}
          </p>
        )}
      </div>

      {items.length > 1 && (
        <div className="flex gap-2 overflow-x-auto border-t border-border bg-muted/20 px-3 py-3">
          {items.map((item, index) => (
            <button
              key={`${item.screen.name}-${index}`}
              type="button"
              onClick={() => setActiveIndex(index)}
              aria-label={t(($) => $.design_proposal.show_screen, {
                name: item.screen.name,
              })}
              aria-current={index === activeIndex ? "true" : undefined}
              className={`group w-24 shrink-0 rounded-lg p-1 text-left outline-none transition-colors sm:w-28 ${
                index === activeIndex
                  ? "bg-primary/10 ring-2 ring-primary"
                  : "hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50"
              }`}
            >
              <DesignScreenImage
                item={item}
                noRenderLabel={noRenderLabel}
                className="aspect-video w-full rounded-md object-cover"
              />
              <span className="mt-1 block truncate px-0.5 text-[10px] font-medium text-muted-foreground group-aria-[current=true]:text-foreground">
                {item.screen.name}
              </span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function DesignScreenImage({
  item,
  noRenderLabel,
  className,
  openLabel,
  onOpen,
}: {
  item: DesignScreenItem;
  noRenderLabel: string;
  className: string;
  openLabel?: string;
  onOpen?: (attachment: Attachment) => void;
}) {
  const { screen, attachment } = item;
  const rawUrl = attachment
    ? attachment.download_url || attachment.markdown_url || attachment.url
    : "";
  const mediaUrl = resolvePublicFileUrl(rawUrl) ?? rawUrl;
  const displayUrl = useAuthenticatedMediaSrc(mediaUrl, attachment?.id, Boolean(attachment));

  const media = attachment ? (
    <img
      src={displayUrl}
      alt={screen.name}
      width={1280}
      height={800}
      className={className}
    />
  ) : (
    <div
      className={`flex items-center justify-center border border-dashed border-border bg-muted/20 px-3 text-center text-[10px] text-muted-foreground ${className}`}
    >
      {screen.figma_node_id || noRenderLabel}
    </div>
  );

  if (!attachment || !onOpen) return media;
  return (
    <button
      type="button"
      className="block w-full outline-none focus-visible:ring-3 focus-visible:ring-inset focus-visible:ring-white/70"
      onClick={() => onOpen(attachment)}
      aria-label={openLabel}
    >
      {media}
    </button>
  );
}

function ReviewMetric({ icon, value, label }: { icon: ReactNode; value: number; label: string }) {
  return (
    <div className="flex min-w-0 items-center justify-center gap-2 px-3 py-2.5 text-xs">
      <span className="shrink-0 text-muted-foreground">{icon}</span>
      <span className="font-semibold tabular-nums text-foreground">{value}</span>
      <span className="truncate text-muted-foreground">{label}</span>
    </div>
  );
}

function countVerdicts(p: DesignProposal | null): { reuse: number; extend: number; new: number } {
  const c = { reuse: 0, extend: 0, new: 0 };
  if (!p) return c;
  for (const comp of p.components) c[comp.verdict]++;
  return c;
}
