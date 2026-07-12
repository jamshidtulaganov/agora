"use client";

import { useEffect, useState } from "react";
import { useQuery, useQueryClient, useMutation } from "@tanstack/react-query";
import {
  CheckCircle2,
  XCircle,
  RefreshCw,
  Loader2,
  Bug,
  GitBranch,
  ChevronDown,
  MoreHorizontal,
} from "lucide-react";
import { toast } from "sonner";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core";
import { issueDetailOptions, qaEvidenceOptions, testCasesOptions, issueKeys } from "@agora/core/issues/queries";
import { Button } from "@agora/ui/components/ui/button";
import { Textarea } from "@agora/ui/components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@agora/ui/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from "@agora/ui/components/ui/dropdown-menu";
import { cn } from "@agora/ui/lib/utils";
import { useT, useTimeAgo } from "../../i18n";
import { InspectorSection } from "../../layout/inspector-section";
import { StructuredResult } from "../../issues/components/qa-result";
import { PullRequestList } from "../../issues/components/pull-request-list";
import { QAActivityPanel } from "./qa-activity-panel";
import { QALiveBrowser } from "./qa-live-browser";
import { QALiveProgress, useQaRunningTasks } from "./qa-live-progress";
import { QADesignCompare } from "./qa-design-compare";
import { TestCasesPanel } from "./test-cases-panel";
import { verdictIcon, verdictTone, verdictBucket } from "./verdict";
import { FileBugSheet } from "./file-bug-sheet";

// The QA lens — the QA team's instrument surface, re-homed from the old
// dedicated /qa/<issueId> page into the issue cockpit's QA stage
// (docs/sdlc-stage-cockpit-plan.md, phase D). Reached via the SDLC stepper's
// QA stage or `?lens=qa`. The cockpit frame supplies the BreadcrumbHeader
// (identifier/title/agent-chip/pin/actions) and the PropRow rail — this
// component owns only the content pane: the live bay (what the QA agent is
// doing right now + the running app, watched and driven in place) and the
// review column (verdict, test cases, checks, PRs, triage actions). No
// bespoke header, no back-to-queue crumb, no rail-open toggle — the frame
// owns all of that now.
//
// V2 (test-case-centric): user feedback on V1 was that the ONE thing a QA
// engineer needs — which case is running right now, pass/fail, and WHY — was
// buried among too many co-equal panels, and the manual Pass/Fail buttons
// duplicated the automatic qa:pass/qa:fail verdict the agent gate already
// attaches. The review column now reads top-to-bottom in priority order:
// a single verdict chip (state + source + an Override escape hatch) → test
// cases at generous height (the primary instrument) → checks and PRs
// collapsed behind InspectorSection (still one click away, no longer
// competing for space) → activity. The triage bar keeps exactly two primary
// actions (send back / re-run); regression + file-bug move into an overflow
// menu since they're rare compared to send-back.
//
// The live bay is signal-driven, not default-on (most QA is API/unit-level
// and never touches a browser — see QALiveBrowser's own comment for why
// mounting it unconditionally was actively harmful: it auto-connected a CDP
// Chromium and auto-booted a dev server as a side effect of merely opening
// this lens). `open` tracks whether the bay is expanded; it auto-opens the
// moment a QA-squad task starts running (the "watch the agent drive" case —
// the core value), stays open once opened (a run ending shouldn't yank the
// browser out from under a reviewer still inspecting it), and only closes on
// an explicit collapse click, which is sticky for as long as the SAME run
// keeps going. The layout itself flips with it: split when the bay is open
// (browser needs the room), single centered reading column when it's not
// (the review content becomes primary).

type Verdict = "pass" | "fail" | "pending";

// Exported for fuzz testing (qa-lens.fuzz.test.ts) — no behavior change.
export function verdictFromLabels(names: string[]): Verdict {
  if (names.includes("qa:fail")) return "fail";
  if (names.includes("qa:pass")) return "pass";
  return "pending";
}

// The reconciled-state values the backend's service.ReconcileQAState can
// emit (see qa-evidence.ts's QAEvidence.reconciled_state). A value outside
// this set — "" (no evidence yet / an old server) or a future enum member
// this build doesn't know about — is treated as "not provided" so the chip
// falls back to its legacy label-derived computation instead of rendering
// something unrecognized.
const KNOWN_RECONCILED_STATES = new Set([
  "running",
  "pass",
  "fail",
  "blocked",
  "stale",
  "never_ran",
  "pass_with_failing_cases",
]);

export function QALensBody({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const [bugOpen, setBugOpen] = useState(false);
  // The send-back Dialog's own open state — the repro/rationale textarea now
  // lives inside it instead of an always-visible field cluttering the triage
  // bar. `note` itself stays lifted here (not local to the dialog) so it
  // keeps pre-filling FileBugSheet exactly as before, whether or not the
  // dialog is currently open.
  const [sendBackOpen, setSendBackOpen] = useState(false);
  const [note, setNote] = useState("");
  // Override dialog (Phase 2 — human override with provenance): picking
  // Mark pass / Mark fail no longer fires bare label calls from the client;
  // it opens a compact reason dialog (same pattern as send-back) and POSTs a
  // proper override that records WHO and WHY server-side — evidence row with
  // source="human", the reason as summary, and a timeline comment. Non-null
  // = the dialog is open for that verdict.
  const [overrideVerdict, setOverrideVerdict] = useState<"pass" | "fail" | null>(null);
  const [overrideReason, setOverrideReason] = useState("");

  const { data: issue, isLoading } = useQuery(issueDetailOptions(wsId, issueId));
  const { data: evidence } = useQuery(qaEvidenceOptions(issueId));
  const { data: labelCatalog } = useQuery({
    queryKey: ["labels", wsId],
    queryFn: () => api.listLabels(),
    staleTime: 60_000,
  });

  // Live-bay signal: is a QA-squad agent task running on this issue right
  // now? Same filtered list QALiveProgress watches for markers — one source,
  // so the two can't disagree (see useQaRunningTasks).
  const qaRunning = useQaRunningTasks(issueId).length > 0;
  // Modality gate (phase 2): the live browser is only WARRANTED when the run
  // can actually drive one — at least one case declares modality "ui", or the
  // suite predates modality entirely (every case is "" — legacy ⇒ keep the
  // old always-open behavior; same when the issue has no cases at all). An
  // issue whose cases are all api/unit/manual never auto-boots a browser; the
  // manual "Open live testing" affordance (QALiveBrowser's onOpen) stays
  // available regardless. While the list is still LOADING the gate holds
  // closed (undefined ≠ warranted) — auto-opening before the modalities are
  // known would boot a Chromium for an api-only issue in the window before
  // the query resolves, which is exactly what the gate exists to prevent.
  const { data: lensCasesData } = useQuery(testCasesOptions(issueId));
  const lensCases = lensCasesData?.test_cases;
  const browserWarranted =
    lensCases !== undefined &&
    (lensCases.length === 0 ||
      lensCases.some((c) => c.modality === "ui") ||
      lensCases.every((c) => !c.modality));
  // Bay open/closed. Starts closed; auto-opens on a run starting (when the
  // browser is warranted) and then stays open (a run ending doesn't
  // auto-collapse it) until the reviewer explicitly collapses it, which
  // sticks for as long as THIS run continues (the effect only flips it back
  // open on a fresh transition).
  const [bayOpen, setBayOpen] = useState(false);
  useEffect(() => {
    if (qaRunning && browserWarranted) setBayOpen(true);
  }, [qaRunning, browserWarranted]);

  const labelId = (name: string) => labelCatalog?.labels.find((l) => l.name === name)?.id;
  const issueLabelNames = (issue?.labels ?? []).map((l) => l.name);
  const humanVerdict = verdictFromLabels(issueLabelNames);

  // Triage: set the human verdict by attaching the chosen qa:* label and
  // detaching the opposite. Mirrors the cockpit's label-derived lanes, so the
  // queue re-groups the moment QA decides. Still used by send-back (a fail
  // verdict as part of the send-back flow, whose own dialog captures the
  // note); the Override dropdown now goes through the provenance-recording
  // override mutation below instead.
  const setVerdict = useMutation({
    mutationFn: async (next: "pass" | "fail") => {
      const addId = labelId(next === "pass" ? "qa:pass" : "qa:fail");
      const removeId = labelId(next === "pass" ? "qa:fail" : "qa:pass");
      if (!addId) throw new Error(`Label qa:${next} not found in this workspace`);
      if (removeId && issueLabelNames.includes(next === "pass" ? "qa:fail" : "qa:pass")) {
        await api.detachLabel(issueId, removeId);
      }
      await api.attachLabel(issueId, addId);
    },
    onSuccess: (_d, next) => {
      void qc.invalidateQueries({ queryKey: issueKeys.detail(wsId, issueId) });
      void qc.invalidateQueries({ queryKey: ["qa-cockpit", wsId] });
      // The reconciled chip state is computed server-side FROM these same
      // labels (service.ReconcileQAState) — without this the chip would keep
      // showing the pre-override state until some unrelated refetch happened.
      void qc.invalidateQueries({ queryKey: issueKeys.qaEvidence(issueId) });
      toast.success(next === "pass" ? t(($) => $.qa_review.marked_pass) : t(($) => $.qa_review.marked_fail));
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Failed"),
  });

  // The provenance-recording override (POST /api/issues/{id}/qa-override):
  // one attributed server-side decision — label flip + human-sourced evidence
  // row + timeline comment — instead of two bare label calls that left the
  // /qa queue showing the agent's stale summary on an overridden row.
  const override = useMutation({
    mutationFn: ({ verdict, reason }: { verdict: "pass" | "fail"; reason: string }) =>
      api.overrideQAVerdict(issueId, { verdict, reason: reason.trim() || undefined }),
    onSuccess: (_d, { verdict }) => {
      setOverrideVerdict(null);
      setOverrideReason("");
      void qc.invalidateQueries({ queryKey: issueKeys.detail(wsId, issueId) });
      void qc.invalidateQueries({ queryKey: issueKeys.qaEvidence(issueId) });
      void qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
      void qc.invalidateQueries({ queryKey: ["qa-cockpit", wsId] });
      void qc.invalidateQueries({ queryKey: ["qa-verdicts", wsId] });
      toast.success(verdict === "pass" ? t(($) => $.qa_review.marked_pass) : t(($) => $.qa_review.marked_fail));
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Failed"),
  });

  const rerun = useMutation({
    mutationFn: () => api.sliceAction(issueId, { kind: "run_qa" }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: issueKeys.tasks(issueId) });
      void qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
      toast.success(t(($) => $.slice_actions.toast_fired));
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : t(($) => $.slice_actions.toast_failed)),
  });

  // One-click "send back to dev": mark the fail verdict AND move the issue to
  // in_progress so it leaves the QA queue and re-enters the dev loop — a
  // hidden 3-hop generic-menu detour in the old flow.
  const sendBack = useMutation({
    mutationFn: async () => {
      await setVerdict.mutateAsync("fail");
      // Attach the QA engineer's note as a comment BEFORE the status flip, so the
      // repro/rationale is on the thread the moment the issue re-enters the dev
      // loop. Skipped when the note is blank (no empty comments).
      const trimmed = note.trim();
      if (trimmed) await api.createComment(issueId, trimmed);
      await api.updateIssue(issueId, { status: "in_progress" });
    },
    onSuccess: () => {
      setSendBackOpen(false);
      setNote("");
      void qc.invalidateQueries({ queryKey: issueKeys.detail(wsId, issueId) });
      void qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
      void qc.invalidateQueries({ queryKey: ["qa-cockpit", wsId] });
      toast.success(t(($) => $.qa_review.sent_back));
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Failed"),
  });

  // Fires the SAME whole-branch regression the sprint-end scheduler runs
  // automatically, manually — from this issue's sprint, without navigating
  // to a separate sprint admin surface. 404 (issue not on a sprint, or no
  // sprint-end autopilot configured) surfaces via the error toast.
  const runRegression = useMutation({
    mutationFn: () => api.runIssueSprintRegression(issueId),
    onSuccess: () => toast.success(t(($) => $.qa_review.run_regression_fired)),
    onError: (e) =>
      toast.error(
        e instanceof Error && e.message ? e.message : t(($) => $.qa_review.run_regression_failed),
      ),
  });

  const verdict = evidence?.verdict ?? "";
  // Effective verdict shown by the chip: the human/override label wins once
  // set, otherwise the agent's automated evidence verdict. Same precedence
  // V1 used for the (now-removed) Pass/Fail button highlight — the chip is
  // that same signal made into the ONE place it's shown, instead of two.
  const suggestedVerdict = humanVerdict !== "pending" ? humanVerdict : verdict;
  const chipVerdict: Verdict =
    suggestedVerdict === "pass" || suggestedVerdict === "fail" ? suggestedVerdict : "pending";

  // Reconciled chip state (Phase 2 — service.ReconcileQAState on the
  // backend): folds labels + per-case run results + a live task into ONE
  // richer enum than pass/fail/pending, so the chip can show
  // "pass_with_failing_cases" (amber — a qa:pass label sitting on a
  // known-failing case, NOT a clean pass) and distinguish blocked / stale /
  // running / never_ran instead of lumping them all into "pending". Only
  // trusted when the server actually populated it (a non-empty, recognized
  // value) — "" covers both "no evidence row yet" and an OLD SERVER that
  // predates this field; either way this falls back to the legacy
  // pass/fail/pending chip above, with the live-run signal layered on top so
  // a running gate still reads as "running" rather than a stale "pending".
  const chipVerdictAsState: string = chipVerdict === "pending" ? "never_ran" : chipVerdict;
  const serverReconciledState = evidence?.reconciled_state ?? "";
  const reconciledState = KNOWN_RECONCILED_STATES.has(serverReconciledState)
    ? serverReconciledState
    : qaRunning
      ? "running"
      : chipVerdictAsState;

  // Fold the reconciled enum onto ONE of the four plain buckets. The chip
  // headline is derived from the bucket, so it can only ever read Passed /
  // Failed / Testing… / Not tested yet — never the seven-way enum label. The
  // nuance the richer enum carried (pass-with-failing / blocked / stale)
  // survives as a muted secondary line below, not as a competing loud color.
  const chipBucket = verdictBucket(reconciledState);

  // Failing-case count for the "N still failing" caveat — read from the SAME
  // test-cases fetch the live-bay gate already uses (lensCases) rather than
  // widening the API response to also carry a count. `checkCount` is the total
  // for the muted "· N checks" detail on the chip's primary line.
  const failingCaseCount = (lensCases ?? []).filter((c) => c.latest_run?.status === "fail").length;
  const checkCount = lensCases?.length ?? 0;

  // Provenance pill: shown ONLY when a human overrode the agent — then it reads
  // "Overridden by you" in place of the old always-on AGENT pill (a clean
  // agent verdict now shows no pill at all). A human override writes the
  // evidence row with source="human"; the label-vs-verdict divergence
  // (isOverride) is the fallback for legacy rows written before the override
  // endpoint existed (label flipped, evidence row still says source=agent).
  const isOverride = humanVerdict !== "pending" && humanVerdict !== verdict;
  const overridden = evidence?.source === "human" || isOverride;

  // One state word, derived from the bucket — the whole point of 7→4.
  const verdictHeadline =
    chipBucket === "pass"
      ? t(($) => $.qa_evidence.verdict_pass)
      : chipBucket === "fail"
        ? t(($) => $.qa_evidence.verdict_fail)
        : chipBucket === "running"
          ? t(($) => $.qa_evidence.verdict_running)
          : t(($) => $.qa_evidence.not_tested);

  // The single muted secondary line: the pass-with-failing / stale caveat, or
  // the fail/blocked reason. A clean pass / pending / running shows no second
  // line — the full summary stays reachable as the chip's hover title.
  const verdictSecondary =
    reconciledState === "pass_with_failing_cases"
      ? t(($) => $.qa_evidence.still_failing, { count: failingCaseCount })
      : reconciledState === "stale"
        ? t(($) => $.qa_evidence.out_of_date)
        : chipBucket === "fail"
          ? evidence?.summary || undefined
          : undefined;

  if (isLoading || !issue) {
    return (
      <div className="flex-1 overflow-y-auto">
        <div className="w-full px-8 py-8">
          <p className="text-sm text-muted-foreground">{t(($) => $.timeline.loading)}</p>
        </div>
      </div>
    );
  }

  const canFileBug = verdict === "fail" || humanVerdict === "fail";

  return (
    <div className="flex-1 overflow-y-auto">
      <div className="w-full px-8 py-8">
        {/* Adaptive split: when the live bay is OPEN (a QA run is live, or
            the reviewer opted in), it takes ALL remaining width on the LEFT
            (it drives a real Chromium pinned at a 1280×800 CDP frame, so it
            needs the room, not a squeezed sidebar) — a narrow, fixed-width
            review column (evidence you read, test cases, the call you make)
            sits on the RIGHT. Below lg the bay stacks ABOVE the review column.
            When the bay is CLOSED (the common case — most QA is API/unit-
            level, no browser involved), the split disappears: the review
            column takes the full width, with the compact bay card above it.
            No centered reading cap here — this is an instrument surface (case
            checklists, run tables, evidence), not prose; the same reason the
            /qa dashboard tabs dropped theirs. */}
        <div
          className={cn(
            bayOpen
              ? "lg:grid lg:grid-cols-[minmax(0,1fr)_380px] lg:items-start lg:gap-6"
              : "flex w-full flex-col gap-6",
          )}
        >
          {/* ── Live bay ─────────────────────────────────────────────────
              Both "live" surfaces together: the terminal feed of what the QA
              agent is doing right now, and the running app itself, watched
              and driven in place. QALiveProgress stays mounted regardless of
              bayOpen — it's the signal source (drives the auto-open effect
              above) and its marker-watching must keep feeding the Test-cases
              panel even while the browser pane is collapsed. It renders
              nothing itself when no run is active. QALiveBrowser owns its own
              open/closed rendering via the `active` prop — see its comment. */}
          <aside
            className={cn(
              "flex flex-col gap-3",
              bayOpen && "order-2 h-[440px] lg:sticky lg:top-8 lg:order-1 lg:h-[calc(100vh-10rem)]",
            )}
          >
            <div className="shrink-0">
              <QALiveProgress issueId={issueId} />
            </div>
            <QALiveBrowser
              issueId={issueId}
              active={bayOpen}
              running={qaRunning}
              onOpen={() => setBayOpen(true)}
              onCollapse={() => setBayOpen(false)}
            />
          </aside>

          {/* ── Review column ────────────────────────────────────────────
              A fixed-height flex column when the bay is open (matching its
              height): one scroll region holds verdict/test-cases/checks/PRs,
              the triage bar pins to the bottom. When the bay is closed, this
              reads as a normal page — no artificial height cap, no sticky
              positioning. Priority order top→bottom: verdict chip, test
              cases (the primary instrument, generous height), checks and PRs
              collapsed behind InspectorSection, then activity. */}
          <div
            className={cn(
              "flex min-w-0 flex-col",
              bayOpen && "order-1 lg:order-2 lg:sticky lg:top-8 lg:h-[calc(100vh-10rem)] lg:min-h-0",
            )}
          >
            {/* Single scroll region above the triage bar — a long (27-case)
                list or a tall checks table scrolls here instead of pushing the
                triage actions off-screen. Capped on mobile, where the
                column isn't height-bounded. */}
            <div className="flex min-h-0 flex-1 flex-col overflow-y-auto pr-0.5 max-lg:max-h-[65vh]">
              {/* Verdict chip — ONE clean line: [icon] Passed · 12 checks ·
                  ran 2m ago  [Override ▾]. One state word, one color, the rest
                  muted. The provenance pill appears only on a human override
                  ("Overridden by you"); a clean agent verdict shows none.
                  Replaces the old hero card AND the seven-way enum label. */}
              <div className="pb-4">
                <div
                  className={cn("rounded-lg border px-3 py-2", verdictTone(reconciledState))}
                  title={evidence?.summary || undefined}
                >
                  <div className="flex items-center gap-2">
                    {verdictIcon(reconciledState, "size-4 shrink-0")}
                    <span className="text-sm font-medium">{verdictHeadline}</span>
                    {checkCount > 0 && (
                      <span className="shrink-0 text-[11px] text-muted-foreground">
                        · {t(($) => $.qa_evidence.checks_count, { count: checkCount })}
                      </span>
                    )}
                    {evidence?.captured_at && (
                      <span
                        className="truncate text-[11px] text-muted-foreground"
                        title={new Date(evidence.captured_at).toLocaleString()}
                      >
                        · {timeAgo(evidence.captured_at)}
                      </span>
                    )}
                    <div className="ml-auto flex shrink-0 items-center gap-2">
                      {overridden && (
                        <span className="rounded-full border px-1.5 py-0.5 text-[10px] text-muted-foreground">
                          {t(($) => $.qa_review.overridden_by_you)}
                        </span>
                      )}
                      <DropdownMenu>
                        <DropdownMenuTrigger
                          render={
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              disabled={override.isPending}
                              className="h-6 gap-1 px-2 text-[11px] text-muted-foreground"
                            />
                          }
                        >
                          {t(($) => $.qa_review.override)}
                          <ChevronDown className="size-3" />
                        </DropdownMenuTrigger>
                        {/* Override is the "agent was wrong, force pass" escape
                            hatch — the fail path is "Send back to dev" in the
                            triage bar, so the menu offers only Mark pass. */}
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem onClick={() => setOverrideVerdict("pass")}>
                            <CheckCircle2 className="size-3.5" />
                            {t(($) => $.qa_review.override_pass)}
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </div>
                  </div>
                  {verdictSecondary && (
                    <p
                      className="mt-1 line-clamp-2 text-[11px] text-muted-foreground"
                      title={verdictSecondary}
                    >
                      {verdictSecondary}
                    </p>
                  )}
                </div>
              </div>

              {/* Test cases — the QA team's instruments (author / generate /
                  run) AND the primary focal point of this lens: which case is
                  running right now, pass/fail, and why. Generous height, no
                  collapse — everything else below defers to it. */}
              <div className="border-t pt-4 pb-4">
                <TestCasesPanel issueId={issueId} />
              </div>

              {/* Checks — collapsed by default. Still the deterministic
                  command-level evidence, just no longer competing with test
                  cases for the reviewer's first glance. */}
              <div className="border-t pt-2 pb-2">
                <InspectorSection title={t(($) => $.qa_review.checks)} defaultOpen={false}>
                  {evidence?.result && (evidence.result.commands.length > 0 || evidence.result.design) ? (
                    <section>
                      {evidence.result.commands.length > 0 && (
                        <div className="rounded-lg border">
                          <StructuredResult result={evidence.result} />
                        </div>
                      )}
                      {evidence.result.design && (
                        <div className={evidence.result.commands.length > 0 ? "mt-3" : ""}>
                          <QADesignCompare design={evidence.result.design} issueId={issueId} />
                        </div>
                      )}
                    </section>
                  ) : (
                    <div className="rounded-lg border border-dashed bg-muted/20 px-3 py-5 text-center">
                      <p className="text-[12px] text-muted-foreground">{t(($) => $.qa_evidence.empty)}</p>
                      <p className="mt-0.5 text-[11px] text-muted-foreground/70">{t(($) => $.qa_evidence.empty_hint)}</p>
                    </div>
                  )}
                </InspectorSection>
              </div>

              {/* Pull requests — dev-side state (checks, conflicts, merge
                  status), one click away rather than always-open real estate. */}
              <div className="border-t pt-2 pb-2">
                <InspectorSection title={t(($) => $.detail.section_pull_requests)} defaultOpen={false}>
                  <div className="rounded-lg border px-2 py-1.5">
                    <PullRequestList issueId={issueId} />
                  </div>
                </InspectorSection>
              </div>

              {/* Discussion — the issue's conversation, inside the lens. QA
                  lives in the thread (repro notes, agent replies, Bitrix-synced
                  comments), and @mentioning an agent here is how a re-check
                  gets dispatched — no hop back to the issue lens. Same
                  timeline cache + comment machinery as issue-detail
                  (stage-cockpit phase G). */}
              <div className="border-t pt-4 pb-4">
                <InspectorSection
                  title={t(($) => $.detail.activity_section)}
                  defaultOpen
                >
                  <QAActivityPanel issueId={issueId} />
                </InspectorSection>
              </div>
            </div>
            {/* /scroll region */}

            {/* Triage bar — pinned to the bottom of the column (flex,
                shrink-0) so the primary actions are always within reach while
                the region above scrolls. Exactly two primary actions (send
                back / re-run); regression + file-bug are rarer, so they live
                behind the overflow menu instead of taking a whole row. */}
            <div className="shrink-0 border-t bg-background pt-4 pb-1">
              <div className="flex items-center gap-1.5">
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  className="h-8 flex-1 gap-1.5 text-[12px]"
                  onClick={() => setSendBackOpen(true)}
                >
                  <XCircle className="size-3.5" />
                  {t(($) => $.qa_review.send_back)}
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  className="h-8 flex-1 gap-1.5 text-[12px]"
                  disabled={rerun.isPending}
                  onClick={() => rerun.mutate()}
                >
                  {rerun.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}
                  {t(($) => $.qa_evidence.rerun)}
                </Button>
                <DropdownMenu>
                  <DropdownMenuTrigger
                    render={
                      <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        className="h-8 w-8 shrink-0"
                        title={t(($) => $.qa_review.more_actions)}
                        aria-label={t(($) => $.qa_review.more_actions)}
                      />
                    }
                  >
                    <MoreHorizontal className="size-3.5" />
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem disabled={runRegression.isPending} onClick={() => runRegression.mutate()}>
                      <GitBranch className="size-3.5" />
                      {t(($) => $.qa_review.run_regression)}
                    </DropdownMenuItem>
                    {canFileBug && (
                      <DropdownMenuItem onClick={() => setBugOpen(true)}>
                        <Bug className="size-3.5 text-destructive" />
                        {t(($) => $.qa_bug.file_bug)}
                      </DropdownMenuItem>
                    )}
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>
            </div>
          </div>
        </div>

        {/* Send-back dialog — the repro/rationale note, entered only when
            actually sending back instead of sitting always-visible in the
            triage bar. `note` stays lifted in the parent so it keeps
            pre-filling FileBugSheet below even after this dialog closes. */}
        <Dialog open={sendBackOpen} onOpenChange={setSendBackOpen}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{t(($) => $.qa_review.send_back)}</DialogTitle>
              <DialogDescription>{t(($) => $.qa_review.send_back_dialog_desc)}</DialogDescription>
            </DialogHeader>
            <Textarea
              value={note}
              onChange={(e) => setNote(e.target.value)}
              rows={4}
              aria-label={t(($) => $.qa_review.note_label)}
              placeholder={t(($) => $.qa_review.note_ph)}
              className="text-[13px]"
            />
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => setSendBackOpen(false)}
              >
                {t(($) => $.qa_review.cancel)}
              </Button>
              <Button
                type="button"
                size="sm"
                className="gap-1.5"
                disabled={sendBack.isPending}
                onClick={() => sendBack.mutate()}
              >
                {sendBack.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <XCircle className="size-3.5" />}
                {t(($) => $.qa_review.send_back_confirm)}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>

        {/* Override dialog (Phase 2) — the WHY behind a human override,
            captured at decision time and recorded server-side (evidence row
            source="human" + timeline comment). Same compact pattern as
            send-back. The reason is optional — a human may just disagree —
            but the dialog makes providing one the path of least resistance. */}
        <Dialog
          open={overrideVerdict !== null}
          onOpenChange={(open) => {
            if (!open) {
              setOverrideVerdict(null);
              setOverrideReason("");
            }
          }}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>
                {overrideVerdict === "pass"
                  ? t(($) => $.qa_review.override_pass)
                  : t(($) => $.qa_review.override_fail)}
              </DialogTitle>
              <DialogDescription>{t(($) => $.qa_review.override_dialog_desc)}</DialogDescription>
            </DialogHeader>
            <Textarea
              value={overrideReason}
              onChange={(e) => setOverrideReason(e.target.value)}
              rows={4}
              aria-label={t(($) => $.qa_review.override_reason_label)}
              placeholder={t(($) => $.qa_review.override_reason_ph)}
              className="text-[13px]"
            />
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => {
                  setOverrideVerdict(null);
                  setOverrideReason("");
                }}
              >
                {t(($) => $.qa_review.cancel)}
              </Button>
              <Button
                type="button"
                size="sm"
                className="gap-1.5"
                disabled={override.isPending}
                onClick={() => {
                  if (overrideVerdict) override.mutate({ verdict: overrideVerdict, reason: overrideReason });
                }}
              >
                {override.isPending ? (
                  <Loader2 className="size-3.5 animate-spin" />
                ) : overrideVerdict === "pass" ? (
                  <CheckCircle2 className="size-3.5" />
                ) : (
                  <XCircle className="size-3.5" />
                )}
                {t(($) => $.qa_review.override_confirm)}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>

        <FileBugSheet
          open={bugOpen}
          onOpenChange={setBugOpen}
          sourceId={issueId}
          sourceTitle={issue.title}
          identifier={issue.identifier}
          projectId={issue.project_id}
          evidence={evidence}
          seedNotes={note}
        />
      </div>
    </div>
  );
}
