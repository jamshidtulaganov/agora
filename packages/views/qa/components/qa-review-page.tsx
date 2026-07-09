"use client";

import { useState } from "react";
import { useQuery, useQueryClient, useMutation } from "@tanstack/react-query";
import { CheckCircle2, XCircle, RefreshCw, Loader2, Bug, GitBranch, Pin, PinOff, MoreHorizontal, PanelRightOpen, PanelRightClose } from "lucide-react";
import { toast } from "sonner";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core";
import { useWorkspacePaths } from "@agora/core/paths";
import { issueDetailOptions, qaEvidenceOptions, issueKeys } from "@agora/core/issues/queries";
import { Button, buttonVariants } from "@agora/ui/components/ui/button";
import { Textarea } from "@agora/ui/components/ui/textarea";
import { Tooltip, TooltipTrigger, TooltipContent } from "@agora/ui/components/ui/tooltip";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { AppLink } from "../../navigation";
import { BreadcrumbHeader, type BreadcrumbSegment } from "../../layout/breadcrumb-header";
import { StructuredResult } from "../../issues/components/qa-result";
import { IssueAgentHeaderChip } from "../../issues/components/issue-agent-header-chip";
import { PullRequestList } from "../../issues/components/pull-request-list";
import { useIssueActions, IssueActionsDropdown } from "../../issues/actions";
import { QALiveBrowser } from "./qa-live-browser";
import { QALiveProgress } from "./qa-live-progress";
import { QADesignCompare } from "./qa-design-compare";
import { TestCasesPanel } from "./test-cases-panel";
import { verdictIcon, verdictTone } from "./verdict";
import { FileBugSheet } from "./file-bug-sheet";

// The QA team's own instrument surface — a DEDICATED page (not the dev-oriented
// issue detail). Reached from the QA cockpit: a row opens /qa/<issueId> here.
// It surfaces only what QA needs to triage: the deterministic verdict (command
// table with new-failure attribution), and the triage actions (pass / fail /
// re-run). No Prompts / Editor / Repository dev chrome.

type Verdict = "pass" | "fail" | "pending";

function verdictFromLabels(names: string[]): Verdict {
  if (names.includes("qa:fail")) return "fail";
  if (names.includes("qa:pass")) return "pass";
  return "pending";
}

export function QAReviewPage({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceId();
  const wp = useWorkspacePaths();
  const qc = useQueryClient();
  const { t } = useT("issues");
  const [bugOpen, setBugOpen] = useState(false);
  // A free-text QA note attached at triage time — posted as a comment on
  // send-back (the dev's repro/rationale trail) and seeded into a filed bug so
  // the engineer doesn't retype it. Optional; empty = today's behavior.
  const [note, setNote] = useState("");
  // Review rail: open by default — the verdict, checks and Pass/Fail all live
  // here, and the audit's top ergonomics finding was that every single open
  // cost a click before triage could start. The live browser still gets the
  // majority of the width; QA can collapse the rail when it wants the full
  // CDP frame, and the collapsed toggle carries a verdict dot.
  const [railOpen, setRailOpen] = useState(true);

  const { data: issue, isLoading } = useQuery(issueDetailOptions(wsId, issueId));
  const actions = useIssueActions(issue ?? null);
  const { data: evidence } = useQuery(qaEvidenceOptions(issueId));
  const { data: labelCatalog } = useQuery({
    queryKey: ["labels", wsId],
    queryFn: () => api.listLabels(),
    staleTime: 60_000,
  });

  const labelId = (name: string) => labelCatalog?.labels.find((l) => l.name === name)?.id;
  const issueLabelNames = (issue?.labels ?? []).map((l) => l.name);
  const humanVerdict = verdictFromLabels(issueLabelNames);

  // Triage: set the human verdict by attaching the chosen qa:* label and
  // detaching the opposite. Mirrors the cockpit's label-derived lanes, so the
  // queue re-groups the moment QA decides.
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

  // Fires the SAME whole-branch regression the sprint-end scheduler runs
  // automatically, manually — from this issue's sprint, without navigating
  // to a separate sprint admin surface. 404 (issue not on a sprint, or no
  // sprint-end autopilot configured) surfaces via the error toast; the button
  // doesn't pre-check sprint membership to avoid an extra round trip for a
  // state the toast already explains clearly.
  // One-click "send back to dev": mark the fail verdict AND move the issue to
  // in_progress so it leaves the QA queue and re-enters the dev loop — the
  // audit found this daily action was a hidden 3-hop generic-menu detour.
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
      setNote("");
      void qc.invalidateQueries({ queryKey: issueKeys.detail(wsId, issueId) });
      void qc.invalidateQueries({ queryKey: issueKeys.timeline(issueId) });
      void qc.invalidateQueries({ queryKey: ["qa-cockpit", wsId] });
      toast.success(t(($) => $.qa_review.sent_back));
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Failed"),
  });

  const runRegression = useMutation({
    mutationFn: () => api.runIssueSprintRegression(issueId),
    onSuccess: () => toast.success(t(($) => $.qa_review.run_regression_fired)),
    onError: (e) =>
      toast.error(
        e instanceof Error && e.message ? e.message : t(($) => $.qa_review.run_regression_failed),
      ),
  });

  const verdict = evidence?.verdict ?? "";
  // Pre-select the triage button matching the AGENT's automated verdict when a
  // human hasn't decided yet — so a reviewer sees "Passed" already reflected at
  // the bottom instead of two blank buttons after the checks already ran. A
  // human click still writes the real qa:pass/qa:fail label and takes over
  // (humanVerdict, once set, always wins over the agent's suggestion).
  const suggestedVerdict = humanVerdict !== "pending" ? humanVerdict : verdict;
  const verdictLabel =
    verdict === "pass"
      ? t(($) => $.qa_evidence.verdict_pass)
      : verdict === "fail"
        ? t(($) => $.qa_evidence.verdict_fail)
        : t(($) => $.qa_evidence.verdict_unknown);

  const breadcrumbSegments: BreadcrumbSegment[] = [
    { href: wp.qa(), label: t(($) => $.qa_review.queue_crumb) },
  ];

  return (
    <div className="flex w-full flex-col">
      {/* Same top bar as the issue detail page — breadcrumb, identifier + title
          as the clickable leaf (opens the full issue), pin / actions menu. QA
          is a dedicated surface for the same issue, not a different entity, so
          its chrome should read identically. */}
      <BreadcrumbHeader
        segments={breadcrumbSegments}
        leaf={
          issue ? (
            <AppLink
              href={wp.issueDetail(issueId)}
              className="flex min-w-0 transition-opacity hover:opacity-80"
            >
              <span className="truncate font-medium text-foreground">
                {issue.identifier} {issue.title}
              </span>
            </AppLink>
          ) : (
            <span className="truncate text-muted-foreground">{t(($) => $.timeline.loading)}</span>
          )
        }
        actions={
          issue ? (
            <>
              <Tooltip>
                <TooltipTrigger
                  className={buttonVariants({
                    variant: railOpen ? "secondary" : "ghost",
                    size: "sm",
                    className: "gap-1.5 text-muted-foreground",
                  })}
                  onClick={() => setRailOpen((v) => !v)}
                >
                  {railOpen ? <PanelRightClose /> : <PanelRightOpen />}
                  <span className="text-[12px]">{t(($) => $.qa_review.review_panel)}</span>
                  {!railOpen && suggestedVerdict !== "pending" && (
                    <span
                      aria-hidden
                      className={cn(
                        "size-1.5 rounded-full",
                        suggestedVerdict === "pass" ? "bg-emerald-500" : "bg-destructive",
                      )}
                    />
                  )}
                </TooltipTrigger>
                <TooltipContent side="bottom">
                  {railOpen ? t(($) => $.qa_review.review_panel_hide) : t(($) => $.qa_review.review_panel_show)}
                </TooltipContent>
              </Tooltip>
              <IssueAgentHeaderChip issueId={issueId} />
              <Tooltip>
                <TooltipTrigger
                  className={buttonVariants({
                    variant: "ghost",
                    size: "icon-sm",
                    className: cn("text-muted-foreground", actions.isPinned && "text-foreground"),
                  })}
                  onClick={actions.togglePin}
                >
                  {actions.isPinned ? <PinOff /> : <Pin />}
                </TooltipTrigger>
                <TooltipContent side="bottom">
                  {actions.isPinned ? t(($) => $.detail.unpin_tooltip) : t(($) => $.detail.pin_tooltip)}
                </TooltipContent>
              </Tooltip>
              <IssueActionsDropdown
                issue={issue}
                align="end"
                onDeletedNavigateTo={wp.qa()}
                trigger={
                  <Button variant="ghost" size="icon-sm" className="text-muted-foreground">
                    <MoreHorizontal />
                  </Button>
                }
              />
            </>
          ) : undefined
        }
      />

      <div className="mx-auto w-full max-w-[1800px] px-6 py-6">
      {isLoading || !issue ? (
        <p className="text-sm text-muted-foreground">{t(($) => $.timeline.loading)}</p>
      ) : (
        // Cockpit split, matching the issue detail page's own convention: the
        // Live testing bay takes ALL remaining width on the LEFT (it drives a
        // real Chromium pinned at a 1280×800 CDP frame, so it needs the room,
        // not a squeezed sidebar) — a narrow, fixed-width review rail
        // (evidence you read, test cases, the call you make) sits on the
        // RIGHT, same as Properties/Details/Prompts on the issue page. Below
        // lg the bay stacks ABOVE the rail (what's running first, then what
        // you decide).
        <div
          className={cn(
            "lg:grid lg:items-start lg:gap-6",
            railOpen ? "lg:grid-cols-[minmax(0,1fr)_420px]" : "lg:grid-cols-1",
          )}
        >
          {/* ── Live bay ─────────────────────────────────────────────────
              Both "live" surfaces together: the terminal feed of what the QA
              agent is doing right now, and the running app itself, watched
              and driven in place. The terminal renders nothing when idle, so
              an idle issue's bay is just the browser. */}
          <aside className="order-2 flex h-[440px] flex-col gap-3 lg:sticky lg:top-6 lg:order-1 lg:h-[calc(100vh-6.5rem)]">
            {/* NOT a tool-call terminal — just a slim "which test case is
                running" strip (renders nothing when idle). The reviewer WATCHES
                the run in the live browser below (the agent shares that Chromium
                over CDP during a scripted run); per-case verdicts are in the
                Test-cases panel. The browser is the star and keeps all the
                height. */}
            <div className="shrink-0">
              <QALiveProgress issueId={issueId} />
            </div>
            <QALiveBrowser issueId={issueId} />
          </aside>

          {/* ── Review rail ───────────────────────────────────────────────
              Collapsed by default (see railOpen); the header toggle opens it.
              A fixed-height flex column on lg (matching the live bay): one
              scroll region holds PRs/verdict/checks/test-cases, the triage bar
              pins to the bottom. Sections separated by hairlines (border-t +
              pt), matching the issue detail page's grouping. */}
          {railOpen && (
          <div className="order-1 flex min-w-0 flex-col lg:order-2 lg:sticky lg:top-6 lg:h-[calc(100vh-6.5rem)] lg:min-h-0">
            {/* Single scroll region above the triage bar — a long (27-case)
                list or a tall checks table scrolls here instead of pushing the
                pass/fail buttons off-screen. Capped on mobile, where the rail
                isn't height-bounded. */}
            <div className="flex min-h-0 flex-1 flex-col overflow-y-auto pr-0.5 max-lg:max-h-[65vh]">
            {/* Pull requests — dev-side state (checks, conflicts, merge status)
                right alongside QA's own verdict, not a separate tab to chase.
                First item in the rail now (title moved to the page's top bar),
                so no top divider. */}
            <section className="pb-4">
              <div className="mb-1.5 text-[11px] uppercase tracking-wide text-muted-foreground">
                {t(($) => $.detail.section_pull_requests)}
              </div>
              <div className="rounded-lg border px-2 py-1.5">
                <PullRequestList issueId={issueId} />
              </div>
            </section>

            {/* Verdict — compact: one line (icon + label + captured time), with
                the summary clamped to two lines (full text on hover). Was an
                oversized hero that pushed the checks/cases below the fold. */}
            <div className="border-t pt-3 pb-3">
              <div className={cn("rounded-lg border px-3 py-2", verdictTone(verdict))}>
                <div className="flex items-center gap-2">
                  {verdictIcon(verdict, "size-4 shrink-0")}
                  <span className="text-sm font-medium">{verdictLabel}</span>
                  {/* Provenance: who produced this verdict — a real agent run vs
                      machinery — so an auto-state can't masquerade as a tested
                      regression (audit P1). "" from older rows = agent. */}
                  {evidence && (
                    <span className="shrink-0 rounded-full border px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
                      {evidence.source || "agent"}
                    </span>
                  )}
                  {evidence?.captured_at && (
                    <span className="ml-auto shrink-0 text-[10px] text-muted-foreground">
                      {new Date(evidence.captured_at).toLocaleString()}
                    </span>
                  )}
                </div>
                {evidence?.summary && (
                  <p className="mt-1 line-clamp-2 text-[11px] text-muted-foreground" title={evidence.summary}>
                    {evidence.summary}
                  </p>
                )}
              </div>
            </div>

            {/* Checks */}
            <div className="border-t pt-4 pb-4">
              {evidence?.result && (evidence.result.commands.length > 0 || evidence.result.design) ? (
                <section>
                  {evidence.result.commands.length > 0 && (
                    <>
                      <div className="mb-1.5 text-[11px] uppercase tracking-wide text-muted-foreground">
                        {t(($) => $.qa_review.checks)}
                      </div>
                      <div className="rounded-lg border">
                        <StructuredResult result={evidence.result} />
                      </div>
                    </>
                  )}
                  {evidence.result.design && (
                    <div className={evidence.result.commands.length > 0 ? "mt-3" : ""}>
                      <QADesignCompare design={evidence.result.design} />
                    </div>
                  )}
                </section>
              ) : (
                <div className="rounded-lg border border-dashed bg-muted/20 px-3 py-5 text-center">
                  <p className="text-[12px] text-muted-foreground">{t(($) => $.qa_evidence.empty)}</p>
                  <p className="mt-0.5 text-[11px] text-muted-foreground/70">{t(($) => $.qa_evidence.empty_hint)}</p>
                </div>
              )}
            </div>

            {/* Test cases — the QA team's instruments (author / generate / run). */}
            <div className="border-t pt-4 pb-4">
              <TestCasesPanel issueId={issueId} />
            </div>
            </div>{/* /scroll region */}

            {/* Triage bar — the QA team's verdict + actions. Pinned to the
                bottom of the rail (flex, shrink-0) so the pass/fail call is
                always within reach while the region above scrolls. Two explicit
                rows — Pass/Fail primary, Regression/Re-run secondary — instead
                of flex-wrap, which at this width wrapped unpredictably. */}
            <div className="shrink-0 space-y-1.5 border-t bg-background pt-4 pb-1">
              <div className="grid grid-cols-2 gap-1.5">
                <Button
                  type="button"
                  size="sm"
                  className={cn(
                    "h-8 gap-1.5 text-[12px]",
                    suggestedVerdict === "pass" && "border-emerald-600/40 bg-emerald-600/10 text-emerald-700 dark:text-emerald-300",
                  )}
                  variant="outline"
                  disabled={setVerdict.isPending}
                  onClick={() => setVerdict.mutate("pass")}
                >
                  <CheckCircle2 className="size-3.5" />
                  {t(($) => $.qa_review.pass)}
                </Button>
                <Button
                  type="button"
                  size="sm"
                  className={cn(
                    "h-8 gap-1.5 text-[12px]",
                    suggestedVerdict === "fail" && "border-destructive/40 bg-destructive/10 text-destructive",
                  )}
                  variant="outline"
                  disabled={setVerdict.isPending}
                  onClick={() => setVerdict.mutate("fail")}
                >
                  <XCircle className="size-3.5" />
                  {t(($) => $.qa_review.fail)}
                </Button>
              </div>
              <div className="grid grid-cols-2 gap-1.5">
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  className="h-8 gap-1.5 text-[12px]"
                  disabled={runRegression.isPending}
                  onClick={() => runRegression.mutate()}
                >
                  {runRegression.isPending ? (
                    <Loader2 className="size-3.5 animate-spin" />
                  ) : (
                    <GitBranch className="size-3.5" />
                  )}
                  {t(($) => $.qa_review.run_regression)}
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  className="h-8 gap-1.5 text-[12px]"
                  disabled={rerun.isPending}
                  onClick={() => rerun.mutate()}
                >
                  {rerun.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}
                  {t(($) => $.qa_evidence.rerun)}
                </Button>
              </div>
              {/* Optional QA note — the reviewer's repro/rationale. Posted as a
                  comment on send-back and seeded into a filed bug. */}
              <Textarea
                value={note}
                onChange={(e) => setNote(e.target.value)}
                rows={2}
                aria-label={t(($) => $.qa_review.note_label)}
                placeholder={t(($) => $.qa_review.note_ph)}
                className="min-h-0 resize-none text-[12px]"
              />
              <Button
                type="button"
                size="sm"
                variant="outline"
                className="h-8 w-full gap-1.5 text-[12px]"
                disabled={sendBack.isPending}
                onClick={() => sendBack.mutate()}
              >
                {sendBack.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <XCircle className="size-3.5" />}
                {t(($) => $.qa_review.send_back)}
              </Button>
              {(verdict === "fail" || humanVerdict === "fail") && (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  className="h-8 w-full gap-1.5 text-[12px]"
                  onClick={() => setBugOpen(true)}
                >
                  <Bug className="size-3.5 text-destructive" />
                  {t(($) => $.qa_bug.file_bug)}
                </Button>
              )}
            </div>
          </div>
          )}

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
      )}
      </div>
    </div>
  );
}
