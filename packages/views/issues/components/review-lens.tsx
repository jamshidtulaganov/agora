"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  AlertTriangle,
  ChevronDown,
  Info,
  Loader2,
  OctagonAlert,
  Rocket,
} from "lucide-react";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core";
import { issueDetailOptions, issueKeys } from "@agora/core/issues/queries";
import { issuePullRequestsOptions } from "@agora/core/github";
import { useWorkspacePaths } from "@agora/core/paths";
import type { MergeGateStatus, ReviewFinding, ReviewVerdict } from "@agora/core/types";
import { Badge } from "@agora/ui/components/ui/badge";
import { Button } from "@agora/ui/components/ui/button";
import { Textarea } from "@agora/ui/components/ui/textarea";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { AppLink } from "../../navigation";
import { ActorAvatar } from "../../common/actor-avatar";
import { PullRequestList } from "./pull-request-list";
import { verdictIcon, verdictTone } from "../../qa/components/verdict";

// The Review lens v2 — the human half of "agent reviews, human approves"
// (docs/review-stage-plan.md). Same wide two-column workbench shape as the
// QA/Dev/Design lenses.
// LEFT (1fr, primary): the agent's code-review verdict card (vibe altitude:
// verdict + plain-language summary + finding counts), the human decision bar
// (Approve & merge / Request changes → POST review-decision, a human-only
// endpoint), the findings list (engineer altitude: severity / file:line /
// detail), then the issue's PR list.
// RIGHT (~380px): the deterministic merge-readiness gate grid (the "review"
// gate row appears automatically from the endpoint once the tier requires
// it), tier, the merge:override badge, and the sprint-deploy pointer.
// Shares the ["merge-readiness", issueId] query key with EditorGates so the
// cache (and the 15s poll) is shared, not duplicated.

type IssuesT = ReturnType<typeof useT<"issues">>["t"];

function GateCard({ gate }: { gate: MergeGateStatus }) {
  const { t } = useT("issues");
  const label = gateStatusLabel(gate.status, t);

  return (
    <div className={cn("rounded-lg border px-3 py-2", verdictTone(gate.status))}>
      <div className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        {gate.name}
      </div>
      <div className="mt-1 flex items-center gap-1.5 text-xs font-medium">
        {verdictIcon(gate.status, "size-3.5 shrink-0")}
        {label}
      </div>
    </div>
  );
}

function gateStatusLabel(status: string, t: IssuesT): string {
  return status === "pass"
    ? t(($) => $.qa_evidence.verdict_pass)
    : status === "fail"
      ? t(($) => $.qa_evidence.verdict_fail)
      : t(($) => $.qa_evidence.verdict_unknown);
}

// Severity visuals — blocker is the only gate-failing kind (destructive),
// major/minor are advisory. Any unrecognized future severity renders with
// the muted "minor" look and its raw label (enum drift downgrades).
function severityMeta(severity: string): {
  Icon: typeof Info;
  badgeCls: string;
} {
  if (severity === "blocker") {
    return { Icon: OctagonAlert, badgeCls: "bg-destructive/10 text-destructive" };
  }
  if (severity === "major") {
    return {
      Icon: AlertTriangle,
      badgeCls: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
    };
  }
  return { Icon: Info, badgeCls: "bg-muted text-muted-foreground" };
}

function severityLabel(severity: string, t: IssuesT): string {
  return severity === "blocker"
    ? t(($) => $.review_lens.severity_blocker)
    : severity === "major"
      ? t(($) => $.review_lens.severity_major)
      : severity === "minor"
        ? t(($) => $.review_lens.severity_minor)
        : severity;
}

const SEVERITY_ORDER: Record<string, number> = { blocker: 0, major: 1, minor: 2 };

function FindingRow({ finding }: { finding: ReviewFinding }) {
  const { t } = useT("issues");
  const { Icon, badgeCls } = severityMeta(finding.severity);

  const header = (
    <>
      <span
        className={cn(
          "inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-medium",
          badgeCls,
        )}
      >
        <Icon className="size-3" />
        {severityLabel(finding.severity, t)}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block text-xs font-medium leading-snug">{finding.title}</span>
        {finding.file && (
          <code className="block truncate font-mono text-[11px] text-muted-foreground">
            {finding.file}
            {finding.line !== null ? `:${finding.line}` : ""}
          </code>
        )}
      </span>
    </>
  );

  if (!finding.detail) {
    return (
      <li data-testid="review-finding" className="flex items-start gap-2 px-3 py-2">
        {header}
      </li>
    );
  }
  return (
    <li data-testid="review-finding">
      <details className="group px-3 py-2">
        <summary className="flex cursor-pointer list-none items-start gap-2 [&::-webkit-details-marker]:hidden">
          {header}
          <ChevronDown className="mt-0.5 size-3.5 shrink-0 text-muted-foreground transition-transform group-open:rotate-180" />
        </summary>
        <p className="mt-1.5 whitespace-pre-wrap text-xs text-muted-foreground">
          {finding.detail}
        </p>
      </details>
    </li>
  );
}

function ReviewVerdictCard({
  review,
  stale,
}: {
  review: ReviewVerdict;
  stale: boolean;
}) {
  const { t } = useT("issues");

  const counts = { blocker: 0, major: 0, minor: 0 };
  for (const f of review.findings) {
    if (f.severity === "blocker") counts.blocker++;
    else if (f.severity === "major") counts.major++;
    else counts.minor++;
  }
  const chips = (
    [
      ["blocker", counts.blocker],
      ["major", counts.major],
      ["minor", counts.minor],
    ] as const
  ).filter(([, n]) => n > 0);

  return (
    <div className={cn("rounded-lg border px-4 py-3", verdictTone(review.verdict))}>
      <div className="flex flex-wrap items-center gap-2">
        {verdictIcon(review.verdict, "size-4 shrink-0")}
        <span className="text-sm font-medium">
          {review.verdict === "pass"
            ? t(($) => $.qa_evidence.verdict_pass)
            : review.verdict === "fail"
              ? t(($) => $.qa_evidence.verdict_fail)
              : review.verdict}
        </span>
        {chips.map(([sev, n]) => {
          const { badgeCls } = severityMeta(sev);
          return (
            <span
              key={sev}
              className={cn("rounded px-1.5 py-0.5 text-[10px] font-medium", badgeCls)}
            >
              {n} × {severityLabel(sev, t)}
            </span>
          );
        })}
      </div>
      {review.summary && (
        <p className="mt-2 text-sm text-muted-foreground">{review.summary}</p>
      )}
      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
        {review.reviewer_agent_id && (
          <ActorAvatar actorType="agent" actorId={review.reviewer_agent_id} size={16} />
        )}
        {review.commit_sha && (
          <span>
            {t(($) => $.review_lens.reviewed_commit)}:{" "}
            <code className="font-mono">{review.commit_sha.slice(0, 7)}</code>
          </span>
        )}
        {review.files_reviewed > 0 && (
          <span>{t(($) => $.review_lens.files_reviewed, { n: review.files_reviewed })}</span>
        )}
      </div>
      {stale && (
        <div className="mt-2 rounded-md bg-amber-500/10 px-2.5 py-1.5 text-[11px] text-amber-700 dark:text-amber-400">
          {t(($) => $.review_lens.stale_hint)}
        </div>
      )}
    </div>
  );
}

export function ReviewLensBody({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceId();
  const wp = useWorkspacePaths();
  const qc = useQueryClient();
  const { t } = useT("issues");

  const [decision, setDecision] = useState<"approve" | "changes" | null>(null);
  const [note, setNote] = useState("");

  const { data: issue } = useQuery(issueDetailOptions(wsId, issueId));
  const { data: readiness, isLoading } = useQuery({
    queryKey: ["merge-readiness", issueId],
    queryFn: () => api.mergeReadiness(issueId),
    enabled: !!issueId,
    refetchInterval: 15000,
  });
  const { data: review, isLoading: reviewLoading } = useQuery({
    queryKey: issueKeys.reviewVerdict(issueId),
    queryFn: () => api.getReviewVerdict(issueId),
    enabled: !!issueId,
  });
  const { data: prData } = useQuery(issuePullRequestsOptions(issueId));

  const hasOverride = (issue?.labels ?? []).some((l) => l.name === "merge:override");
  const hasVerdict = review?.verdict === "pass" || review?.verdict === "fail";

  // Stale hint: the backend PR payload carries no head SHA, so we approximate
  // "the reviewed commit is no longer the PR head" with "an open PR was
  // updated after the review was recorded" — a superset (any PR event bumps
  // pr_updated_at), which errs on the side of prompting a re-run.
  const reviewedAtMs = review?.reviewed_at ? Date.parse(review.reviewed_at) : NaN;
  const stale =
    hasVerdict &&
    Number.isFinite(reviewedAtMs) &&
    (prData?.pull_requests ?? []).some(
      (pr) => pr.state === "open" && Date.parse(pr.pr_updated_at) > reviewedAtMs,
    );

  // merge:override bypasses the deterministic gates by design, so override
  // wins unconditionally; the normal path still needs a clean pass + a ready
  // gate grid.
  const canApprove = hasOverride || (review?.verdict === "pass" && readiness?.ready === true);

  const refreshDecision = () => {
    void qc.invalidateQueries({ queryKey: issueKeys.detail(wsId, issueId) });
    void qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
    void qc.invalidateQueries({ queryKey: issueKeys.reviewVerdict(issueId) });
    void qc.invalidateQueries({ queryKey: ["merge-readiness", issueId] });
  };

  const runReview = useMutation({
    mutationFn: () => api.sliceAction(issueId, { kind: "run_review" }),
    onSuccess: () => {
      toast.success(t(($) => $.review_lens.toast_review_dispatched));
      void qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
    },
    onError: (e) => {
      const msg = e instanceof Error ? e.message : "";
      toast.error(msg || t(($) => $.review_lens.toast_review_dispatch_failed));
    },
  });

  const decide = useMutation({
    mutationFn: (body: { action: "approve" | "request_changes"; note?: string }) =>
      api.reviewDecision(issueId, body),
    onSuccess: (res, vars) => {
      toast.success(
        vars.action === "approve"
          ? // When no squad lead resolves, the backend returns
            // merged_dispatch:false — nothing was dispatched, so tell the
            // human to merge it by hand instead of claiming a dispatch.
            res.merged_dispatch
            ? t(($) => $.review_lens.toast_approved)
            : t(($) => $.review_lens.toast_approved_manual)
          : t(($) => $.review_lens.toast_changes_requested),
      );
      setDecision(null);
      setNote("");
      refreshDecision();
    },
    onError: (e) => {
      const msg = e instanceof Error ? e.message : "";
      toast.error(msg || t(($) => $.review_lens.toast_decision_failed));
    },
  });

  const submitChanges = () => {
    if (!note.trim()) {
      toast.error(t(($) => $.review_lens.changes_note_required));
      return;
    }
    decide.mutate({ action: "request_changes", note: note.trim() });
  };

  const sortedFindings = [...(review?.findings ?? [])].sort(
    (a, b) => (SEVERITY_ORDER[a.severity] ?? 3) - (SEVERITY_ORDER[b.severity] ?? 3),
  );

  return (
    <div className="flex-1 overflow-y-auto">
      <div className="w-full px-8 py-8">
        <div className="lg:grid lg:grid-cols-[minmax(0,1fr)_380px] lg:items-start lg:gap-6">
          {/* Verdict + decision + findings + PR list — the primary content. */}
          <div className="order-2 min-w-0 space-y-6 lg:order-1">
            {/* Agent code-review verdict. */}
            <section>
              <div className="mb-2 flex items-center justify-between gap-2">
                <div className="text-[11px] uppercase tracking-wide text-muted-foreground">
                  {t(($) => $.review_lens.verdict_heading)}
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => runReview.mutate()}
                  disabled={runReview.isPending}
                >
                  {runReview.isPending ? (
                    <Loader2 className="size-3.5 animate-spin" />
                  ) : hasVerdict ? (
                    t(($) => $.review_lens.rerun_review)
                  ) : (
                    t(($) => $.review_lens.run_review)
                  )}
                </Button>
              </div>

              {reviewLoading ? (
                <p className="text-sm text-muted-foreground">{t(($) => $.timeline.loading)}</p>
              ) : !review || !hasVerdict ? (
                <div className="rounded-lg border border-dashed bg-muted/20 px-3 py-5 text-center">
                  <p className="text-xs font-medium">{t(($) => $.review_lens.no_review_title)}</p>
                  <p className="mt-1 text-[12px] text-muted-foreground">
                    {t(($) => $.review_lens.no_review_body)}
                  </p>
                </div>
              ) : (
                <ReviewVerdictCard review={review} stale={stale} />
              )}
            </section>

            {/* Human decision bar — the endpoint is human-only (403 for
                machine actors); the button gating here is UX, the server is
                the authority. */}
            <section className="rounded-lg border px-4 py-3">
              {decision === null ? (
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    size="sm"
                    onClick={() => setDecision("approve")}
                    disabled={!canApprove || decide.isPending}
                  >
                    {t(($) => $.review_lens.approve_merge)}
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => setDecision("changes")}
                    disabled={decide.isPending}
                  >
                    {t(($) => $.review_lens.request_changes)}
                  </Button>
                  {canApprove && (
                    <span className="text-[11px] text-muted-foreground">
                      {t(($) => $.review_lens.awaiting_approval)}
                    </span>
                  )}
                </div>
              ) : decision === "approve" ? (
                <div className="space-y-2">
                  <p className="text-xs text-muted-foreground">
                    {t(($) => $.review_lens.approve_confirm_body)}
                  </p>
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
                    {(readiness?.gates ?? []).map((g) => (
                      <span key={g.name} className="inline-flex items-center gap-1">
                        {verdictIcon(g.status, "size-3 shrink-0")}
                        {g.name}: {gateStatusLabel(g.status, t)}
                      </span>
                    ))}
                  </div>
                  <div className="flex justify-end gap-2">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setDecision(null)}
                      disabled={decide.isPending}
                    >
                      {t(($) => $.review_lens.cancel)}
                    </Button>
                    <Button
                      size="sm"
                      onClick={() => decide.mutate({ action: "approve" })}
                      disabled={decide.isPending}
                    >
                      {decide.isPending ? (
                        <Loader2 className="size-3.5 animate-spin" />
                      ) : (
                        t(($) => $.review_lens.approve_confirm)
                      )}
                    </Button>
                  </div>
                </div>
              ) : (
                <div className="space-y-2">
                  <Textarea
                    value={note}
                    onChange={(e) => setNote(e.target.value)}
                    placeholder={t(($) => $.review_lens.changes_note_placeholder)}
                    className="min-h-[3rem] text-xs"
                  />
                  <div className="flex justify-end gap-2">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setDecision(null)}
                      disabled={decide.isPending}
                    >
                      {t(($) => $.review_lens.cancel)}
                    </Button>
                    <Button size="sm" onClick={submitChanges} disabled={decide.isPending}>
                      {decide.isPending ? (
                        <Loader2 className="size-3.5 animate-spin" />
                      ) : (
                        t(($) => $.review_lens.changes_submit)
                      )}
                    </Button>
                  </div>
                </div>
              )}
            </section>

            {/* Findings — engineer altitude. */}
            {hasVerdict && (
              <section>
                <div className="mb-2 text-[11px] uppercase tracking-wide text-muted-foreground">
                  {t(($) => $.review_lens.findings_heading)}
                  {sortedFindings.length > 0 ? ` (${sortedFindings.length})` : ""}
                </div>
                {sortedFindings.length === 0 ? (
                  <p className="text-[12px] text-muted-foreground">
                    {t(($) => $.review_lens.no_findings)}
                  </p>
                ) : (
                  <ul className="divide-y rounded-lg border">
                    {sortedFindings.map((f, i) => (
                      <FindingRow key={i} finding={f} />
                    ))}
                  </ul>
                )}
              </section>
            )}

            {/* PR list. */}
            <section>
              <div className="mb-2 text-[11px] uppercase tracking-wide text-muted-foreground">
                {t(($) => $.detail.section_pull_requests)}
              </div>
              <div className="rounded-lg border px-2 py-1.5">
                <PullRequestList issueId={issueId} />
              </div>
            </section>
          </div>

          {/* Merge gates + tier + override + deploy pointer. */}
          <div className="order-1 mb-6 lg:order-2 lg:mb-0 [&>*+*]:mt-6 [&>*+*]:border-t [&>*+*]:pt-6">
            <section>
              <div className="mb-2 flex items-center gap-2">
                <div className="text-[11px] uppercase tracking-wide text-muted-foreground">
                  {t(($) => $.review_lens.gates_heading)}
                </div>
                {hasOverride && (
                  <Badge variant="outline">{t(($) => $.review_lens.override_badge)}</Badge>
                )}
              </div>

              {isLoading ? (
                <p className="text-sm text-muted-foreground">{t(($) => $.timeline.loading)}</p>
              ) : !readiness || readiness.gates.length === 0 ? (
                <div className="rounded-lg border border-dashed bg-muted/20 px-3 py-5 text-center">
                  <p className="text-[12px] text-muted-foreground">{t(($) => $.review_lens.empty)}</p>
                </div>
              ) : (
                <div>
                  <div className="grid grid-cols-2 gap-2">
                    {readiness.gates.map((g) => (
                      <GateCard key={g.name} gate={g} />
                    ))}
                  </div>
                  <div className="mt-2 text-[11px] text-muted-foreground">
                    {t(($) => $.review_lens.tier_label)}: {readiness.tier} ·{" "}
                    {readiness.ready
                      ? t(($) => $.review_lens.ready)
                      : t(($) => $.review_lens.blocked)}
                  </div>
                </div>
              )}
            </section>

            {/* Deploy moved to the sprint-level panel (QA cockpit's Sprint
                view) — a compact pointer instead of re-mounting that panel
                here, so this rail stays read-only merge-readiness info. */}
            <section>
              <AppLink
                href={wp.qa()}
                className="flex items-center gap-2 rounded-lg border px-3 py-2 text-[12px] text-muted-foreground transition-colors hover:border-border-strong hover:bg-accent/50 hover:text-foreground"
              >
                <Rocket className="size-3.5 shrink-0" />
                <span className="flex-1">{t(($) => $.review_lens.deploy_note)}</span>
                <span className="shrink-0 font-medium text-foreground">
                  {t(($) => $.review_lens.deploy_link)}
                </span>
              </AppLink>
            </section>
          </div>
        </div>
      </div>
    </div>
  );
}
