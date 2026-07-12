/* eslint-disable i18next/no-literal-string -- design review dialog; i18n follow-up */
"use client";

import { useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ExternalLink, Loader2 } from "lucide-react";
import { api } from "@agora/core/api";
import { issueKeys } from "@agora/core/issues/queries";
import { useWorkspaceId } from "@agora/core/hooks";
import type { DesignProposal, ParsedDesignProposal, DesignVerdict } from "@agora/core/design";
import type { Attachment } from "@agora/core/types";
import { Dialog, DialogContent } from "@agora/ui/components/ui/dialog";
import { Button } from "@agora/ui/components/ui/button";
import { Input } from "@agora/ui/components/ui/input";
import { Textarea } from "@agora/ui/components/ui/textarea";
import { Checkbox } from "@agora/ui/components/ui/checkbox";
import { useT } from "../../i18n";

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

export function DesignReviewDialog({ issueId, versions, onClose }: DesignReviewDialogProps) {
  const { t } = useT("issues");
  const qc = useQueryClient();
  const wsId = useWorkspaceId();

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

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="flex h-[85vh] max-h-[85vh] w-[min(92vw,900px)] max-w-[900px] flex-col gap-0 overflow-hidden p-0">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-border px-5 py-3">
          <h2 className="text-sm font-semibold">{t(($) => $.design_proposal.review_title)}</h2>
          {versions.length > 1 && (
            <select
              className="rounded-md border border-border bg-background px-2 py-1 text-xs"
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
          )}
        </div>

        <div className="flex-1 space-y-5 overflow-y-auto px-5 py-4">
          {!proposal ? (
            <p className="text-sm text-muted-foreground">{t(($) => $.design_proposal.invalid_hint)}</p>
          ) : (
            <>
              {version?.parsed.state === "blocked" && (
                <div className="rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">
                  {t(($) => $.design_proposal.status_blocked)}
                  {proposal.reason ? ` — ${proposal.reason}` : ""}
                </div>
              )}

              {/* Screens gallery */}
              {proposal.screens.length > 0 && (
                <section>
                  <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                    {t(($) => $.design_proposal.screens)} ({proposal.screens.length})
                  </h3>
                  <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
                    {proposal.screens.map((screen, i) => {
                      const att = attachmentFor(screen.render);
                      return (
                        <figure key={i} className="space-y-1">
                          {att ? (
                            <a href={att.download_url || att.url} target="_blank" rel="noreferrer">
                              <img
                                src={att.download_url || att.url}
                                alt={screen.name}
                                className="aspect-video w-full rounded-md border border-border object-cover"
                              />
                            </a>
                          ) : (
                            <div className="flex aspect-video w-full items-center justify-center rounded-md border border-dashed border-border text-[10px] text-muted-foreground">
                              {screen.figma_node_id || t(($) => $.design_proposal.no_render)}
                            </div>
                          )}
                          <figcaption className="truncate text-xs font-medium">{screen.name}</figcaption>
                          {screen.summary && (
                            <p className="line-clamp-2 text-[11px] text-muted-foreground">{screen.summary}</p>
                          )}
                        </figure>
                      );
                    })}
                  </div>
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

              {proposal.figma.length > 0 && (
                <div className="flex flex-wrap gap-2">
                  {proposal.figma.map((f, i) => (
                    <a
                      key={i}
                      href={f.url}
                      target="_blank"
                      rel="noreferrer"
                      className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
                    >
                      <ExternalLink className="size-3" /> {t(($) => $.figma_links.open_in_figma)}
                    </a>
                  ))}
                </div>
              )}
            </>
          )}
        </div>

        {/* Triage bar */}
        <div className="space-y-2 border-t border-border px-5 py-3">
          <Textarea
            value={note}
            onChange={(e) => setNote(e.target.value)}
            placeholder={t(($) => $.design_proposal.changes_note_placeholder)}
            className="min-h-[2.5rem] text-xs"
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
            <div className="flex justify-end gap-2">
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
              <Button size="sm" onClick={() => setConfirmApprove(true)} disabled={!!submitting || !proposal || version?.parsed.state === "blocked"}>
                {t(($) => $.design_proposal.approve)}
              </Button>
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function countVerdicts(p: DesignProposal | null): { reuse: number; extend: number; new: number } {
  const c = { reuse: 0, extend: 0, new: 0 };
  if (!p) return c;
  for (const comp of p.components) c[comp.verdict]++;
  return c;
}
