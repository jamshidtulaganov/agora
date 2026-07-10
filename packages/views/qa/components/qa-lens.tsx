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
import { useT } from "../../i18n";
import { InspectorSection } from "../../layout/inspector-section";
import { StructuredResult } from "../../issues/components/qa-result";
import { PullRequestList } from "../../issues/components/pull-request-list";
import { QAActivityPanel } from "./qa-activity-panel";
import { QALiveBrowser } from "./qa-live-browser";
import { QALiveProgress, useQaRunningTasks } from "./qa-live-progress";
import { QADesignCompare } from "./qa-design-compare";
import { TestCasesPanel } from "./test-cases-panel";
import { verdictIcon, verdictTone } from "./verdict";
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

export function QALensBody({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const { t } = useT("issues");
  const [bugOpen, setBugOpen] = useState(false);
  // The send-back Dialog's own open state — the repro/rationale textarea now
  // lives inside it instead of an always-visible field cluttering the triage
  // bar. `note` itself stays lifted here (not local to the dialog) so it
  // keeps pre-filling FileBugSheet exactly as before, whether or not the
  // dialog is currently open.
  const [sendBackOpen, setSendBackOpen] = useState(false);
  const [note, setNote] = useState("");

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
  // queue re-groups the moment QA decides. Also the Override dropdown's
  // mutation — an "override" is just this same call made when the label
  // state doesn't already match the agent's automated verdict.
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
      toast.success(next === "pass" ? t(($) => $.qa_review.marked_pass) : t(($) => $.qa_review.marked_fail));
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
  // Provenance for the chip: a label that diverges from the agent's own
  // evidence verdict can only have gotten there via a human's Override click
  // (the auto-gate always attaches the label matching its own verdict) — so
  // that divergence IS the "human touched this" signal, with no extra state
  // to track.
  const isOverride = humanVerdict !== "pending" && humanVerdict !== verdict;
  const chipSource = chipVerdict === "pending" ? null : isOverride ? "human" : evidence?.source || "agent";
  const verdictLabel =
    chipVerdict === "pass"
      ? t(($) => $.qa_evidence.verdict_pass)
      : chipVerdict === "fail"
        ? t(($) => $.qa_evidence.verdict_fail)
        : t(($) => $.qa_evidence.verdict_unknown);

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
            column becomes the primary reading column at a comfortable
            centered measure, with the compact bay card sitting above it. */}
        <div
          className={cn(
            bayOpen
              ? "lg:grid lg:grid-cols-[minmax(0,1fr)_380px] lg:items-start lg:gap-6"
              : "mx-auto flex w-full max-w-4xl flex-col gap-6",
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
              {/* Verdict chip — compact: icon + label + source pill + an
                  Override escape hatch, all in one row. Replaces the old
                  hero card AND the separate Pass/Fail buttons that used to
                  live in the triage bar — those two were showing the same
                  fact twice. */}
              <div className="pb-4">
                <div className={cn("rounded-lg border px-3 py-2", verdictTone(chipVerdict))}>
                  <div className="flex items-center gap-2">
                    {verdictIcon(chipVerdict, "size-4 shrink-0")}
                    <span className="text-sm font-medium">{verdictLabel}</span>
                    {chipSource && (
                      <span className="shrink-0 rounded-full border px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
                        {chipSource === "human" ? t(($) => $.qa_review.source_human) : t(($) => $.qa_review.source_agent)}
                      </span>
                    )}
                    <DropdownMenu>
                      <DropdownMenuTrigger
                        render={
                          <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            disabled={setVerdict.isPending}
                            className="ml-auto h-6 gap-1 px-2 text-[11px] text-muted-foreground"
                          />
                        }
                      >
                        {t(($) => $.qa_review.override)}
                        <ChevronDown className="size-3" />
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem onClick={() => setVerdict.mutate("pass")}>
                          <CheckCircle2 className="size-3.5" />
                          {t(($) => $.qa_review.override_pass)}
                        </DropdownMenuItem>
                        <DropdownMenuItem onClick={() => setVerdict.mutate("fail")}>
                          <XCircle className="size-3.5" />
                          {t(($) => $.qa_review.override_fail)}
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </div>
                  {(evidence?.summary || evidence?.captured_at) && (
                    <div className="mt-1 flex items-center gap-1.5">
                      {evidence?.summary && (
                        <p className="line-clamp-2 text-[11px] text-muted-foreground" title={evidence.summary}>
                          {evidence.summary}
                        </p>
                      )}
                      {evidence?.captured_at && (
                        <span className="ml-auto shrink-0 text-[10px] text-muted-foreground">
                          {new Date(evidence.captured_at).toLocaleString()}
                        </span>
                      )}
                    </div>
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
