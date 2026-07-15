"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  CircleDashed,
  Code2,
  Eye,
  ExternalLink,
  Info,
  LayoutDashboard,
  Loader2,
  OctagonAlert,
  Rocket,
  ShieldCheck,
} from "lucide-react";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core";
import {
  issueDetailOptions,
  issueKeys,
  issueOrchestrationOptions,
} from "@agora/core/issues/queries";
import { issuePullRequestsOptions } from "@agora/core/github";
import { useWorkspacePaths } from "@agora/core/paths";
import type { MergeGateStatus, ReviewFinding, ReviewVerdict } from "@agora/core/types";
import { Badge } from "@agora/ui/components/ui/badge";
import { Button } from "@agora/ui/components/ui/button";
import { Textarea } from "@agora/ui/components/ui/textarea";
import {
  NativeSelect,
  NativeSelectOption,
} from "@agora/ui/components/ui/native-select";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@agora/ui/components/ui/tabs";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { AppLink, useNavigation } from "../../navigation";
import { ActorAvatar } from "../../common/actor-avatar";
import { PullRequestList } from "./pull-request-list";
import { verdictIcon, verdictTone } from "../../qa/components/verdict";
import { ArtifactCodeViewer } from "./artifact-code-viewer";
import { ArtifactChecksPanel, ArtifactPreviewPanel } from "./artifact-runtime-panels";
import { QAEvidenceSection } from "./qa-evidence-section";

const REVIEW_TAB_QUERY_KEY = "review_tab";
const REVIEW_TABS = ["overview", "code", "product", "evidence"] as const;
type ReviewTab = (typeof REVIEW_TABS)[number];

function isReviewTab(value: string | null): value is ReviewTab {
  return REVIEW_TABS.some((tab) => tab === value);
}

// Unified Review workspace. The overview is the human decision surface while
// Code, Product, and Evidence expose the exact integrated artifact behind that
// decision. Review and release execution remain DAG-owned: this surface can
// approve the persisted release step or create a versioned correction cycle,
// but it never creates parallel side tasks.

type IssuesT = ReturnType<typeof useT<"issues">>["t"];

function GateCard({ gate }: { gate: MergeGateStatus }) {
  const { t } = useT("issues");
  const label = gateStatusLabel(gate.status, t);

  return (
    <div className={cn("rounded-lg border px-3 py-2", verdictTone(gate.status))}>
      <div className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        {plainGateName(gate.name, t)}
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

// Gate names arrive as backend slugs ("ci" | "qa" | "security" | "review" |
// "code-review"). Never show the slug — map to plain English. Unknown future
// gate slugs render their raw name (enum drift downgrades, not crashes).
function plainGateName(name: string, t: IssuesT): string {
  switch (name) {
    case "ci":
      return t(($) => $.review_lens.gate_ci);
    case "qa":
      return t(($) => $.review_lens.gate_qa);
    case "security":
      return t(($) => $.review_lens.gate_security);
    case "review":
    case "code-review":
      return t(($) => $.review_lens.gate_review);
    default:
      return name;
  }
}

function plainGateState(status: string, t: IssuesT): string {
  return status === "fail"
    ? t(($) => $.review_lens.gate_state_failed)
    : t(($) => $.review_lens.gate_state_pending);
}

type BannerKind = "merging" | "ready" | "review_pending" | "blocking";

// One conclusion, computed once. The banner is the single "can this merge?"
// answer — the gate grid + reasons are the inputs, moved behind a Details
// disclosure. Tone maps to the four states; the reasons array is the "why".
function ReadinessBanner({
  kind,
  blockerCount,
  reasons,
}: {
  kind: BannerKind;
  blockerCount: number;
  reasons: string[];
}) {
  const { t } = useT("issues");

  const meta: Record<
    BannerKind,
    { Icon: typeof CheckCircle2; cls: string; spin?: boolean; label: string }
  > = {
    ready: {
      Icon: CheckCircle2,
      cls: "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
      label: t(($) => $.review_lens.ready),
    },
    merging: {
      Icon: Loader2,
      cls: "border-blue-500/30 bg-blue-500/10 text-blue-700 dark:text-blue-400",
      spin: true,
      label: t(($) => $.review_lens.banner_merging),
    },
    review_pending: {
      Icon: CircleDashed,
      cls: "border-border bg-muted/30 text-muted-foreground",
      label: t(($) => $.review_lens.banner_review_pending),
    },
    blocking: {
      Icon: AlertTriangle,
      cls: "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400",
      label: t(($) => $.review_lens.banner_blocking, { n: blockerCount }),
    },
  };
  const { Icon, cls, spin, label } = meta[kind];

  return (
    <div className={cn("rounded-lg border px-3.5 py-3", cls)}>
      <div className="flex items-center gap-2">
        <Icon className={cn("size-4 shrink-0", spin && "animate-spin")} />
        <span className="text-sm font-semibold">{label}</span>
      </div>
      {kind === "blocking" && reasons.length > 0 && (
        <ul className="mt-1.5 space-y-0.5 pl-6 text-[12px] text-muted-foreground">
          {reasons.map((r, i) => (
            <li key={i} className="list-disc">
              {r}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
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
  diffUrl,
}: {
  review: ReviewVerdict;
  stale: boolean;
  diffUrl?: string;
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
        {diffUrl && (
          <a
            href={diffUrl}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 font-medium text-primary hover:underline"
          >
            {t(($) => $.review_lens.view_diff)}
            <ExternalLink className="size-3" />
          </a>
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
  const navigation = useNavigation();
  const qc = useQueryClient();
  const { t } = useT("issues");
  const requestedTab = navigation.searchParams.get(REVIEW_TAB_QUERY_KEY);
  const activeTab: ReviewTab = isReviewTab(requestedTab) ? requestedTab : "overview";

  const [decision, setDecision] = useState<"approve" | "changes" | null>(null);
  const [note, setNote] = useState("");
  const [targetStepId, setTargetStepId] = useState("");

  const { data: issue } = useQuery(issueDetailOptions(wsId, issueId));
  const { data: orchestration, isLoading: orchestrationLoading } = useQuery(
    issueOrchestrationOptions(issueId),
  );
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
  const correctionTargets = [...(orchestration?.steps ?? [])]
    .filter(
      (step) =>
        step.stage === "dev" &&
        step.kind === "task" &&
        step.status === "completed" &&
        !!step.agent_id,
    )
    .sort((a, b) => b.position - a.position);
  const selectedTargetId = targetStepId || correctionTargets[0]?.id || "";
  const selectedTarget = correctionTargets.find((step) => step.id === selectedTargetId);
  const hasActivePlanWork = (orchestration?.steps ?? []).some(
    (step) => step.status === "queued" || step.status === "running",
  );
  const requestChangesBlockedReason = orchestrationLoading
    ? t(($) => $.review_lens.correction_loading)
    : !orchestration
      ? t(($) => $.review_lens.correction_requires_plan)
      : hasActivePlanWork
        ? t(($) => $.review_lens.correction_wait_for_work)
        : "";

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

  // One-click from the verdict to the diff the agent reviewed: prefer an open
  // PR, else the newest one on the issue. Links to the PR page (provider-
  // agnostic — the diff is one tab away on GitHub/GitLab alike).
  const primaryPr =
    (prData?.pull_requests ?? []).find((pr) => pr.state === "open") ??
    (prData?.pull_requests ?? [])[0];
  const diffUrl = primaryPr?.html_url || undefined;

  // merge:override bypasses the deterministic gates by design, so override
  // wins unconditionally; the normal path still needs a clean pass + a ready
  // gate grid.
  const canApprove = hasOverride || (review?.verdict === "pass" && readiness?.ready === true);
  const releaseStep = [...(orchestration?.steps ?? [])]
    .filter((step) => step.stage === "release")
    .sort((a, b) => b.position - a.position)[0];
  const reviewPlanStep = [...(orchestration?.steps ?? [])]
    .filter((step) => step.stage === "review")
    .sort((a, b) => b.position - a.position)[0];
  const reviewPlanStatus = reviewPlanStep?.status === "completed"
    ? t(($) => $.review_lens.plan_review_completed)
    : reviewPlanStep?.status === "queued" || reviewPlanStep?.status === "running"
      ? t(($) => $.review_lens.plan_review_running)
      : reviewPlanStep?.status === "failed" || reviewPlanStep?.status === "cancelled"
        ? t(($) => $.review_lens.plan_review_failed)
        : t(($) => $.review_lens.plan_review_waiting);
  const releaseWaiting = releaseStep?.status === "waiting_approval";
  const releaseRunning = releaseStep?.status === "queued" || releaseStep?.status === "running";
  const releaseBlockedReason = orchestrationLoading
    ? t(($) => $.review_lens.release_loading)
    : !orchestration
      ? t(($) => $.review_lens.release_requires_plan)
      : !releaseStep
        ? t(($) => $.review_lens.release_gate_missing)
        : releaseRunning
          ? t(($) => $.review_lens.release_in_progress)
          : releaseStep.status === "completed"
            ? t(($) => $.review_lens.release_completed)
            : !releaseWaiting
              ? t(($) => $.review_lens.release_waiting_for_gates)
              : "";

  const setTab = (value: string) => {
    if (!isReviewTab(value)) return;
    const params = new URLSearchParams(navigation.searchParams);
    params.set(REVIEW_TAB_QUERY_KEY, value);
    navigation.replace(`${navigation.pathname}?${params.toString()}`);
  };

  const refreshDecision = () => {
    void qc.invalidateQueries({ queryKey: issueKeys.detail(wsId, issueId) });
    void qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
    void qc.invalidateQueries({ queryKey: issueKeys.reviewVerdict(issueId) });
    void qc.invalidateQueries({ queryKey: issueKeys.orchestration(issueId) });
    void qc.invalidateQueries({ queryKey: issueKeys.artifact(issueId) });
    void qc.invalidateQueries({ queryKey: ["merge-readiness", issueId] });
  };

  const decide = useMutation({
    mutationFn: (body: {
      action: "request_changes";
      note?: string;
      expectedVersion?: number;
      targetStepId?: string;
    }) =>
      api.reviewDecision(issueId, body),
    onSuccess: (res) => {
      toast.success(t(($) => $.review_lens.toast_changes_requested, { version: res.plan_version }));
      setDecision(null);
      setNote("");
      setTargetStepId("");
      refreshDecision();
    },
    onError: (e) => {
      const msg = e instanceof Error ? e.message : "";
      toast.error(msg || t(($) => $.review_lens.toast_decision_failed));
    },
  });

  const approveRelease = useMutation({
    mutationFn: () => api.approveOrchestrationStep(issueId, releaseStep?.id ?? ""),
    onSuccess: () => {
      toast.success(t(($) => $.review_lens.toast_release_started));
      setDecision(null);
      refreshDecision();
    },
    onError: (error) => {
      toast.error(error instanceof Error && error.message ? error.message : t(($) => $.review_lens.toast_decision_failed));
      refreshDecision();
    },
  });

  const submitChanges = () => {
    if (!note.trim()) {
      toast.error(t(($) => $.review_lens.changes_note_required));
      return;
    }
    if (!orchestration || requestChangesBlockedReason) {
      toast.error(requestChangesBlockedReason || t(($) => $.review_lens.correction_requires_plan));
      return;
    }
    decide.mutate({
      action: "request_changes",
      note: note.trim(),
      expectedVersion: orchestration.plan_version,
      ...(selectedTargetId ? { targetStepId: selectedTargetId } : {}),
    });
  };

  const sortedFindings = [...(review?.findings ?? [])].sort(
    (a, b) => (SEVERITY_ORDER[a.severity] ?? 3) - (SEVERITY_ORDER[b.severity] ?? 3),
  );
  const hasBlockerFinding = sortedFindings.some((f) => f.severity === "blocker");

  // The single "why can't this merge" list, derived from the same signals as
  // canApprove so the banner and the Approve button never disagree. Each
  // non-passing gate contributes one plain-English reason; the code-review
  // verdict adds one when the tier didn't already fold it into a gate.
  const gates = readiness?.gates ?? [];
  const hasReviewGate = gates.some((g) => g.name === "review" || g.name === "code-review");
  const blockers: string[] = [];
  if (!hasOverride) {
    for (const g of gates) {
      if (g.status === "pass") continue;
      blockers.push(`${plainGateName(g.name, t)} — ${plainGateState(g.status, t)}`);
    }
    if (!hasReviewGate && review?.verdict !== "pass") {
      blockers.push(
        `${t(($) => $.review_lens.gate_review)} — ${plainGateState(
          hasVerdict ? "fail" : "pending",
          t,
        )}`,
      );
    }
  }

  const releasing = approveRelease.isPending || releaseRunning;
  const bannerKind: BannerKind = releasing
    ? "merging"
    : canApprove
      ? "ready"
      : !hasVerdict
        ? "review_pending"
        : "blocking";
  // The one-line reason to show inline under a disabled Approve button.
  const disabledReason = !hasVerdict
    ? t(($) => $.review_lens.banner_review_pending)
    : (blockers[0] ?? "");

  return (
    <div className="flex h-[calc(100vh-6rem)] min-h-0 w-full flex-col px-4 py-4">
      <Tabs value={activeTab} onValueChange={setTab} className="min-h-0 flex-1 gap-3">
        <div className="flex min-w-0 items-center gap-3 border-b">
          <TabsList variant="line" className="no-scrollbar min-w-0 max-w-full justify-start overflow-x-auto">
            <TabsTrigger value="overview">
              <LayoutDashboard aria-hidden />
              {t(($) => $.review_workspace.overview)}
            </TabsTrigger>
            <TabsTrigger value="code">
              <Code2 aria-hidden />
              {t(($) => $.review_workspace.code)}
            </TabsTrigger>
            <TabsTrigger value="product">
              <Eye aria-hidden />
              {t(($) => $.review_workspace.product)}
            </TabsTrigger>
            <TabsTrigger value="evidence">
              <ShieldCheck aria-hidden />
              {t(($) => $.review_workspace.evidence)}
            </TabsTrigger>
          </TabsList>
        </div>
        <TabsContent value="overview" className="min-h-0 overflow-auto">
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
                {reviewPlanStep && (
                  <Badge variant="outline" className={cn(
                    "font-normal",
                    reviewPlanStep.status === "completed" && "text-success",
                    (reviewPlanStep.status === "queued" || reviewPlanStep.status === "running") && "text-brand",
                    (reviewPlanStep.status === "failed" || reviewPlanStep.status === "cancelled") && "text-destructive",
                  )}>
                    {(reviewPlanStep.status === "queued" || reviewPlanStep.status === "running") && (
                      <span className="size-1.5 rounded-full bg-brand motion-safe:animate-pulse" aria-hidden />
                    )}
                    {reviewPlanStatus}
                  </Badge>
                )}
              </div>

              {reviewLoading ? (
                <p className="text-sm text-muted-foreground">{t(($) => $.timeline.loading)}</p>
              ) : !review || !hasVerdict ? (
                <div className="rounded-lg border border-dashed bg-muted/20 px-3 py-5 text-center">
                  <p className="text-xs font-medium">{t(($) => $.review_lens.no_review_title)}</p>
                  <p className="mt-1 text-[12px] text-muted-foreground">
                    {t(($) => $.review_lens.no_review_plan_body)}
                  </p>
                </div>
              ) : (
                <ReviewVerdictCard review={review} stale={stale} diffUrl={diffUrl} />
              )}
            </section>

            {/* Human decision bar — the endpoint is human-only (403 for
                machine actors); the button gating here is UX, the server is
                the authority. */}
            <section className="rounded-lg border px-4 py-3">
              {decision === null ? (
                <div className="space-y-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <Button
                      size="sm"
                      onClick={() => setDecision("approve")}
                      disabled={!canApprove || !releaseWaiting || decide.isPending || approveRelease.isPending}
                    >
                      {t(($) => $.review_lens.approve_merge)}
                    </Button>
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => setDecision("changes")}
                      disabled={decide.isPending || approveRelease.isPending || !!requestChangesBlockedReason}
                    >
                      {t(($) => $.review_lens.request_changes)}
                    </Button>
                  </div>
                  {/* Always say why: awaiting-you when ready, the top blocker
                      when Approve is disabled (the reason a disabled control
                      must state inline). */}
                  {canApprove && releaseWaiting ? (
                    <p className="text-[11px] text-muted-foreground">
                      {t(($) => $.review_lens.awaiting_approval)}
                    </p>
                  ) : disabledReason ? (
                    <p className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                      <CircleDashed className="size-3 shrink-0" />
                      {disabledReason}
                    </p>
                  ) : null}
                  {releaseBlockedReason && (
                    <p className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                      <CircleDashed className="size-3 shrink-0" aria-hidden />
                      {releaseBlockedReason}
                    </p>
                  )}
                  {requestChangesBlockedReason && (
                    <p className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                      <CircleDashed className="size-3 shrink-0" aria-hidden />
                      {requestChangesBlockedReason}
                    </p>
                  )}
                </div>
              ) : decision === "approve" ? (
                <div className="space-y-2">
                  <p className="text-xs text-muted-foreground">
                    {t(($) => $.review_lens.approve_confirm_body)}
                  </p>
                  <div className="flex justify-end gap-2">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setDecision(null)}
                      disabled={approveRelease.isPending}
                    >
                      {t(($) => $.review_lens.cancel)}
                    </Button>
                    <Button
                      size="sm"
                      onClick={() => approveRelease.mutate()}
                      disabled={approveRelease.isPending || !releaseWaiting}
                    >
                      {approveRelease.isPending ? (
                        <Loader2 className="size-3.5 animate-spin motion-reduce:animate-none" />
                      ) : (
                        t(($) => $.review_lens.approve_confirm)
                      )}
                    </Button>
                  </div>
                </div>
              ) : (
                <div className="space-y-2">
                  <div className="rounded-md border bg-muted/20 px-3 py-2">
                    <p className="text-xs font-medium">
                      {t(($) => $.review_lens.correction_plan_revision, {
                        current: orchestration?.plan_version ?? 0,
                        next: (orchestration?.plan_version ?? 0) + 1,
                      })}
                    </p>
                    <p className="mt-0.5 text-[11px] text-muted-foreground">
                      {t(($) => $.review_lens.correction_cycle_hint)}
                    </p>
                  </div>
                  {correctionTargets.length > 1 ? (
                    <div className="space-y-1">
                      <label htmlFor="review-correction-target" className="text-[11px] font-medium text-muted-foreground">
                        {t(($) => $.review_lens.correction_route_label)}
                      </label>
                      <NativeSelect
                        id="review-correction-target"
                        className="w-full"
                        value={selectedTargetId}
                        onChange={(event) => setTargetStepId(event.target.value)}
                      >
                        {correctionTargets.map((step) => (
                          <NativeSelectOption key={step.id} value={step.id}>
                            {step.title} · {step.capability}
                          </NativeSelectOption>
                        ))}
                      </NativeSelect>
                    </div>
                  ) : selectedTarget ? (
                    <p className="text-[11px] text-muted-foreground">
                      {t(($) => $.review_lens.correction_route_single, { target: selectedTarget.title })}
                    </p>
                  ) : null}
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
                    <Button size="sm" onClick={submitChanges} disabled={decide.isPending || !!requestChangesBlockedReason}>
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

            {/* Findings — engineer altitude. Collapsed by default; only auto-
                expands when there's a blocker (the finding that gates merge). */}
            {hasVerdict && (
              <section>
                {sortedFindings.length === 0 ? (
                  <>
                    <div className="mb-2 text-[11px] uppercase tracking-wide text-muted-foreground">
                      {t(($) => $.review_lens.findings_heading)}
                    </div>
                    <p className="text-[12px] text-muted-foreground">
                      {t(($) => $.review_lens.no_findings)}
                    </p>
                  </>
                ) : (
                  <details open={hasBlockerFinding} className="group">
                    <summary className="mb-2 flex cursor-pointer list-none items-center gap-1.5 text-[11px] uppercase tracking-wide text-muted-foreground [&::-webkit-details-marker]:hidden">
                      <ChevronDown className="size-3.5 transition-transform group-open:rotate-180" />
                      {t(($) => $.review_lens.findings_heading)} ({sortedFindings.length})
                    </summary>
                    <ul className="divide-y rounded-lg border">
                      {sortedFindings.map((f, i) => (
                        <FindingRow key={i} finding={f} />
                      ))}
                    </ul>
                  </details>
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
              ) : (
                <div className="space-y-2">
                  {/* One conclusion up top; the gate breakdown (the inputs)
                      folds behind Details. No tier line — "trivial/light/full"
                      is internal policy, never user-facing. */}
                  <ReadinessBanner
                    kind={bannerKind}
                    blockerCount={blockers.length}
                    reasons={blockers}
                  />
                  {gates.length > 0 && (
                    <details className="group rounded-lg border px-3 py-2">
                      <summary className="flex cursor-pointer list-none items-center gap-1.5 text-[11px] font-medium text-muted-foreground [&::-webkit-details-marker]:hidden">
                        <ChevronDown className="size-3.5 transition-transform group-open:rotate-180" />
                        {t(($) => $.review_lens.details)}
                      </summary>
                      <div className="mt-2 grid grid-cols-2 gap-2">
                        {gates.map((g) => (
                          <GateCard key={g.name} gate={g} />
                        ))}
                      </div>
                    </details>
                  )}
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
        </TabsContent>
        <TabsContent value="code" className="min-h-0 overflow-auto">
          <ArtifactCodeViewer issueId={issueId} className="min-h-[34rem]" />
        </TabsContent>
        <TabsContent value="product" className="min-h-0 overflow-auto">
          <ArtifactPreviewPanel issueId={issueId} />
        </TabsContent>
        <TabsContent value="evidence" className="min-h-0 space-y-4 overflow-auto">
          <QAEvidenceSection issueId={issueId} status={issue?.status ?? "in_review"} allowRerun={false} />
          <ArtifactChecksPanel issueId={issueId} />
        </TabsContent>
      </Tabs>
    </div>
  );
}
