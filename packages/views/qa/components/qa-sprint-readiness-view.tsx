"use client";

import { useRef, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { GitBranch, Rocket, Play, Plus, Loader2, HelpCircle, ChevronDown, ChevronRight, MoreHorizontal, Check, Circle, OctagonAlert } from "lucide-react";
import { api } from "@agora/core/api";
import { sprintReadinessOptions } from "@agora/core/qa/queries";
import type { SprintReadinessResponse } from "@agora/core/api/schemas";
import { useWorkspacePaths } from "@agora/core/paths";
import { useWorkspaceId } from "@agora/core/hooks";
import { Button, buttonVariants } from "@agora/ui/components/ui/button";
import { Badge } from "@agora/ui/components/ui/badge";
import { Card } from "@agora/ui/components/ui/card";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from "@agora/ui/components/ui/dropdown-menu";
import { ProgressRing } from "@agora/ui/components/ui/progress-ring";
import { HoverCard, HoverCardTrigger, HoverCardContent } from "@agora/ui/components/ui/hover-card";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@agora/ui/components/ui/alert-dialog";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { AppLink } from "../../navigation";
import { IssuePickerModal } from "../../modals/issue-picker-modal";
import { SprintDeployPanel } from "./sprint-deploy-panel";
import { regressionStatusMeta } from "./regression-status";
import { sprintReadiness, type ReadinessState } from "./sprint-readiness";
import { verdictIcon } from "./verdict";

// Sprint QA-readiness — "is this sprint ready to ship?" Per active sprint: a
// readiness ring (passed/total, toned by state), the primary ship/blockers
// CTA, a positive-first "what's shipping" changelog, and the sprint deploy
// panel. Answers, at a glance, how close the shared sprint branch is to
// shipping. Reads GET /api/qa/sprint-readiness (shared cache with the health
// strip + queue).

export function QASprintReadinessView({
  projectId,
  onSeeBlockers,
}: {
  projectId?: string;
  // Deep-link to the Queue filtered to needs-human (the health strip's chip
  // pattern) — the "See N blockers" CTA's target. Falls back to the first
  // failing issue's QA lens when the parent doesn't wire it.
  onSeeBlockers?: () => void;
}) {
  const wp = useWorkspacePaths();
  const wsId = useWorkspaceId();
  const { t } = useT("issues");
  // Shared factory — the same cache entry the health strip reads.
  const { data, isLoading } = useQuery(sprintReadinessOptions(wsId, projectId));

  const sprints = data?.sprints ?? [];

  if (isLoading && !data) {
    return (
      <div className="w-full space-y-4 px-8 py-8" aria-hidden>
        <Card className="h-28 w-full" />
        <Card className="h-28 w-full" />
      </div>
    );
  }
  if (sprints.length === 0) {
    return (
      <div className="w-full px-8 py-8">
        <Card className="items-center gap-2 py-12 text-center text-sm text-muted-foreground">
          <Rocket className="size-6 text-muted-foreground/60" aria-hidden />
          <p className="px-6">{t(($) => $.qa_cockpit.sprint_empty)}</p>
        </Card>
      </div>
    );
  }

  return (
    <div className="flex w-full flex-col gap-6 px-8 py-8">
      <ReleaseCommand sprints={sprints} onSeeBlockers={onSeeBlockers} />

      <div className="flex items-center gap-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        <span>{t(($) => $.qa_cockpit.view_ship)}</span>
        <HoverCard>
          <HoverCardTrigger
            render={
              <button
                type="button"
                aria-label={t(($) => $.qa_cockpit.ship_help_label)}
                className="flex size-5 items-center justify-center rounded-full text-muted-foreground hover:bg-accent hover:text-foreground"
              >
                <HelpCircle className="size-3.5" aria-hidden />
              </button>
            }
          />
          <HoverCardContent className="w-72">
            <p className="text-[12px] leading-relaxed text-muted-foreground">{t(($) => $.qa_cockpit.ship_help)}</p>
          </HoverCardContent>
        </HoverCard>
      </div>

      {/* The QA lens lives inside the issue cockpit now (docs/sdlc-stage-cockpit-plan.md
          phase D) — there is no more dedicated /qa/<id> page, so these links
          deep-link to the issue with the QA stage pre-selected. */}
      {sprints.map((s) => (
        <SprintCard
          key={s.sprint_id}
          sprint={s}
          wsId={wsId}
          qaDetail={(id) => `${wp.issueDetail(id)}?lens=qa`}
          onSeeBlockers={onSeeBlockers}
        />
      ))}
    </div>
  );
}

type SprintData = SprintReadinessResponse["sprints"][number];

type ReleasePathState = "pass" | "active" | "fail" | "pending";

function ReleasePathStep({
  label,
  state,
  last,
}: {
  label: string;
  state: ReleasePathState;
  last?: boolean;
}) {
  const Icon = state === "pass" ? Check : state === "fail" ? OctagonAlert : Circle;
  return (
    <div className="flex min-w-0 flex-1 items-center">
      <div className="flex min-w-0 items-center gap-2">
        <span
          className={cn(
            "flex size-6 shrink-0 items-center justify-center rounded-full border",
            state === "pass" && "border-emerald-500/40 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
            state === "fail" && "border-destructive/40 bg-destructive/10 text-destructive",
            state === "active" && "border-brand/40 bg-brand/10 text-brand",
            state === "pending" && "border-border bg-background text-muted-foreground/60",
          )}
        >
          <Icon className={cn("size-3", state === "active" && "fill-current")} aria-hidden />
        </span>
        <span className={cn("truncate text-[11px] font-medium", state === "pending" ? "text-muted-foreground" : "text-foreground")}>{label}</span>
      </div>
      {!last && <span className={cn("mx-3 h-px min-w-4 flex-1", state === "pass" ? "bg-emerald-500/30" : "bg-border")} aria-hidden />}
    </div>
  );
}

function ReleaseCommand({
  sprints,
  onSeeBlockers,
}: {
  sprints: SprintData[];
  onSeeBlockers?: () => void;
}) {
  const { t } = useT("issues");
  const total = sprints.reduce((sum, sprint) => sum + sprint.total, 0);
  const passed = sprints.reduce((sum, sprint) => sum + sprint.passed, 0);
  const failed = sprints.reduce((sum, sprint) => sum + sprint.failed, 0);
  const remaining = Math.max(0, total - passed);
  const allReady = sprints.every((sprint) => sprint.mergeable);
  const regressionFailed = sprints.some((sprint) =>
    sprint.regression ? regressionStatusMeta(sprint.regression.status).failed : false,
  );
  const regressionPassed = sprints.every((sprint) =>
    sprint.regression ? regressionStatusMeta(sprint.regression.status).done : false,
  );
  const headline = allReady
    ? t(($) => $.qa_cockpit.release_command_ready)
    : failed > 0
      ? t(($) => $.qa_cockpit.release_command_blocked, { count: failed })
      : t(($) => $.qa_cockpit.release_command_progress, { count: remaining });

  const qaState: ReleasePathState = failed > 0 ? "fail" : remaining === 0 ? "pass" : "active";
  const regressionState: ReleasePathState = regressionFailed
    ? "fail"
    : regressionPassed
      ? "pass"
      : qaState === "pass"
        ? "active"
        : "pending";

  return (
    <section className="overflow-hidden rounded-xl border bg-card shadow-sm">
      <div className="flex flex-col gap-5 px-5 py-5 md:flex-row md:items-start md:justify-between">
        <div className="min-w-0">
          <div className="text-[10px] font-semibold uppercase tracking-[0.16em] text-muted-foreground">
            {t(($) => $.qa_cockpit.release_command_title)}
          </div>
          <h1 className="mt-1 max-w-2xl text-xl font-semibold tracking-tight text-pretty text-foreground">
            {headline}
          </h1>
          <p className="mt-1 text-[12px] text-muted-foreground tabular-nums">
            {t(($) => $.qa_cockpit.health_rollup, {
              ready: sprints.filter((sprint) => sprint.mergeable).length,
              sprints: sprints.length,
            })}
          </p>
        </div>
        {failed > 0 && onSeeBlockers ? (
          <Button size="sm" variant="destructive" onClick={onSeeBlockers} className="shrink-0">
            {t(($) => $.qa_cockpit.see_blockers, { count: failed })}
          </Button>
        ) : null}
      </div>
      <div className="border-t bg-muted/20 px-5 py-3.5">
        <div className="overflow-x-auto">
          <div className="flex min-w-[520px] items-center">
            <ReleasePathStep label={t(($) => $.qa_cockpit.release_path_scope)} state={total > 0 ? "pass" : "pending"} />
            <ReleasePathStep label={t(($) => $.qa_cockpit.release_path_qa)} state={qaState} />
            <ReleasePathStep label={t(($) => $.qa_cockpit.release_path_regression)} state={regressionState} />
            <ReleasePathStep label={t(($) => $.qa_cockpit.release_path_deploy)} state={allReady ? "active" : "pending"} last />
          </div>
        </div>
      </div>
    </section>
  );
}

function RegressionGate({ gate, issueHref }: { gate: SprintData["regression"]; issueHref: (id: string) => string }) {
  const { t } = useT("issues");
  if (!gate || !gate.status) {
    return <span className="text-[11px] text-muted-foreground">{t(($) => $.qa_cockpit.sprint_regression_never_run)}</span>;
  }
  // Shared status classification (regression-status.ts) — the same mapping the
  // sprint-readiness rollup reads, so the two can't drift. The raw server
  // status string never reaches the user; it's mapped to plain English here.
  const { running, failed, done, Icon, className: cls } = regressionStatusMeta(gate.status);
  const label = done
    ? t(($) => $.qa_cockpit.regression_passed)
    : failed
      ? t(($) => $.qa_cockpit.regression_failed)
      : t(($) => $.qa_cockpit.regression_running);
  const body = (
    <>
      <Icon className={`size-3.5 ${running ? "animate-spin" : ""}`} aria-hidden />
      {label}
    </>
  );
  // Click-through to the run's tracking issue — the chip used to be a
  // dead-end (toast + tooltip only) with no way to reach the agent output.
  if (gate.run_issue_id) {
    return (
      <AppLink
        href={issueHref(gate.run_issue_id)}
        className={`flex items-center gap-1 text-[11px] underline-offset-2 hover:underline ${cls}`}
        title={gate.reason || label}
      >
        {body}
      </AppLink>
    );
  }
  return (
    <span className={`flex items-center gap-1 text-[11px] ${cls}`} title={gate.reason || gate.status}>
      {body}
    </span>
  );
}

const SUBLABEL_TONE: Record<ReadinessState, string> = {
  ready: "text-emerald-600 dark:text-emerald-400",
  blocking: "text-destructive",
  regression: "text-amber-600 dark:text-amber-400",
  togo: "text-muted-foreground",
};

function SprintCard({
  sprint: s,
  wsId,
  qaDetail,
  onSeeBlockers,
}: {
  sprint: SprintData;
  wsId: string;
  qaDetail: (id: string) => string;
  onSeeBlockers?: () => void;
}) {
  const { t } = useT("issues");
  const qc = useQueryClient();
  const [attachOpen, setAttachOpen] = useState(false);
  const [changelogOpen, setChangelogOpen] = useState(false);
  // Deploy is collapsed at rest — a Ship card shouldn't open with a full deploy
  // panel in every row. "Ship it" reveals it; a not-ready sprint never shows it.
  const [deployOpen, setDeployOpen] = useState(false);
  const deployRef = useRef<HTMLDivElement>(null);
  // Set only when attaching an issue that already belongs to a DIFFERENT
  // sprint — issue_to_sprint is one-sprint-per-issue (PK = issue_id), so
  // attaching such an issue silently MOVES it. Gated behind an explicit
  // confirm dialog (audit P1: the picker used to offer such issues with no
  // warning at all).
  const [pendingMove, setPendingMove] = useState<{ issueId: string; sprintName: string } | null>(null);

  const readiness = sprintReadiness(s);
  const togo = Math.max(0, s.total - s.passed);
  // Ring sub-label — reads the shared readiness state so the Ship card and the
  // health strip name the same thing.
  const subLabelText =
    readiness.state === "ready"
      ? t(($) => $.qa_cockpit.ready_to_ship)
      : readiness.state === "blocking"
        ? t(($) => $.qa_cockpit.n_blocking, { count: readiness.count })
        : readiness.state === "regression"
          ? t(($) => $.qa_cockpit.regression_pending)
          : t(($) => $.qa_cockpit.n_to_go, { count: readiness.count });

  // Fail-first row order: the rows blocking the merge surface at the top of
  // the changelog (pending next, passed last); stable sort keeps server order
  // within each group.
  const verdictRank = (v: string) => (v === "fail" ? 0 : v === "pass" ? 2 : 1);
  const sortedIssues = [...s.issues].sort((a, b) => verdictRank(a.verdict) - verdictRank(b.verdict));
  const firstFailing = sortedIssues.find((i) => i.verdict === "fail");
  // Changelog groups — positive framing keeps "Passed" first when clean, but
  // fails lead when there's something to fix.
  const changelogGroups = [
    { key: "fail", label: t(($) => $.qa_cockpit.changelog_needs_fix), items: sortedIssues.filter((i) => i.verdict === "fail") },
    { key: "pending", label: t(($) => $.qa_cockpit.changelog_pending), items: sortedIssues.filter((i) => i.verdict !== "fail" && i.verdict !== "pass") },
    { key: "pass", label: t(($) => $.qa_cockpit.changelog_passed), items: sortedIssues.filter((i) => i.verdict === "pass") },
  ].filter((g) => g.items.length > 0);

  const runRegression = useMutation({
    mutationFn: () => api.runSprintRegression(s.sprint_id),
    onSuccess: () => {
      toast.success(t(($) => $.qa_cockpit.sprint_toast_regression_fired));
      void qc.invalidateQueries({ queryKey: ["qa-sprint-readiness"] });
    },
    onError: (e) =>
      toast.error(e instanceof Error && e.message ? e.message : t(($) => $.qa_cockpit.sprint_toast_regression_failed)),
  });
  // Attach an existing project task to this sprint so it joins the sprint's
  // regression scope + mergeability rollup.
  const attachTask = useMutation({
    mutationFn: (issueId: string) => api.assignIssueSprint(issueId, s.sprint_id),
    onSuccess: () => {
      toast.success(t(($) => $.qa_cockpit.sprint_toast_attach_success));
      void qc.invalidateQueries({ queryKey: ["qa-sprint-readiness"] });
    },
    onError: (e) =>
      toast.error(e instanceof Error && e.message ? e.message : t(($) => $.qa_cockpit.sprint_toast_attach_failed)),
  });
  // Checks whether the picked issue already belongs to a different sprint
  // BEFORE attaching — if so, the confirm dialog opens instead of firing the
  // move outright.
  const checkAttach = useMutation({
    mutationFn: async (issueId: string) => {
      const current = await api.getIssueSprint(issueId).catch(() => null);
      return { issueId, current };
    },
    onSuccess: ({ issueId, current }) => {
      if (current && current.id && current.id !== s.sprint_id) {
        setPendingMove({ issueId, sprintName: current.name });
      } else {
        attachTask.mutate(issueId);
      }
    },
  });

  const shipIt = () => {
    // No new backend call — REVEAL the deploy panel (collapsed by default) so
    // the human fires the environment deploy from its existing controls. The
    // panel mounts on this state change, so defer the scroll a frame.
    setDeployOpen(true);
    requestAnimationFrame(() => deployRef.current?.scrollIntoView({ behavior: "smooth", block: "nearest" }));
  };

  return (
    <Card className="gap-0 py-0">
      <div className="flex items-center gap-4 p-4">
        {/* LEFT: the readiness gestalt — ring only. */}
        <ProgressRing
          value={readiness.value}
          size={76}
          strokeWidth={6}
          tone={readiness.tone}
          aria-label={t(($) => $.qa_cockpit.ring_aria, { passed: s.passed, total: s.total })}
        >
          <span className="text-base font-semibold tabular-nums">
            {s.passed}/{s.total}
          </span>
        </ProgressRing>

        {/* MIDDLE: identity + ONE headline state. Branch, the regression chip
            and the verdict-count badges live in "What's shipping" below —
            they're the detail behind the headline, not the headline itself. */}
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="truncate text-[13px] font-semibold">
            {s.project_title} · {s.name}
          </span>
          <span className={cn("text-[13px] font-medium", SUBLABEL_TONE[readiness.state])}>{subLabelText}</span>
        </div>

        {/* RIGHT: one primary CTA + a `⋯` menu for the secondary actions (run
            regression / attach a task) so they don't compete with the CTA. */}
        <div className="flex shrink-0 items-center gap-2">
          {s.mergeable ? (
            <Button size="sm" className="h-8 gap-1.5" onClick={shipIt}>
              <Rocket className="size-3.5" />
              {t(($) => $.qa_cockpit.ship_it)}
            </Button>
          ) : s.failed > 0 ? (
            onSeeBlockers ? (
              <Button size="sm" variant="destructive" className="h-8 gap-1.5" onClick={onSeeBlockers}>
                {t(($) => $.qa_cockpit.see_blockers, { count: s.failed })}
              </Button>
            ) : (
              <AppLink
                href={qaDetail(firstFailing?.id ?? "")}
                className={cn(buttonVariants({ variant: "destructive", size: "sm" }), "h-8 gap-1.5")}
              >
                {t(($) => $.qa_cockpit.see_blockers, { count: s.failed })}
              </AppLink>
            )
          ) : (
            <Button size="sm" variant="outline" className="h-8 gap-1.5" disabled>
              <Rocket className="size-3.5" />
              {t(($) => $.qa_cockpit.ship_it)}
            </Button>
          )}
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  className="size-8 shrink-0"
                  aria-label={t(($) => $.qa_cockpit.view_more)}
                >
                  <MoreHorizontal className="size-3.5" />
                </Button>
              }
            />
            <DropdownMenuContent align="end">
              <DropdownMenuItem disabled={runRegression.isPending} onClick={() => runRegression.mutate()}>
                {runRegression.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Play className="size-3.5" />}
                {t(($) => $.qa_cockpit.sprint_run_regression)}
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => setAttachOpen(true)}>
                <Plus className="size-3.5" />
                {t(($) => $.qa_cockpit.sprint_attach_tasks)}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>

      <IssuePickerModal
        open={attachOpen}
        onOpenChange={setAttachOpen}
        title={t(($) => $.qa_cockpit.sprint_attach_modal_title)}
        description={t(($) => $.qa_cockpit.sprint_attach_modal_desc, { project: s.project_title, sprint: s.name })}
        projectId={s.project_id}
        excludeIds={s.issues.map((i) => i.id)}
        onSelect={(issue) => checkAttach.mutate(issue.id)}
      />

      <AlertDialog open={!!pendingMove} onOpenChange={(open) => !open && setPendingMove(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.qa_cockpit.sprint_confirm_move_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.qa_cockpit.sprint_confirm_move_body, { name: pendingMove?.sprintName ?? "" })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.qa_cockpit.suite_cancel)}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (pendingMove) attachTask.mutate(pendingMove.issueId);
                setPendingMove(null);
              }}
            >
              {t(($) => $.qa_cockpit.sprint_confirm_move_action)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* "What's shipping" changelog — positive-first summary in the header,
          issues grouped by verdict when expanded. Also the changelog source
          Thread B feeds to Slack/GitHub/GitLab. */}
      {s.issues.length > 0 ? (
        <div className="border-t">
          <button
            type="button"
            onClick={() => setChangelogOpen((v) => !v)}
            className="flex w-full items-center gap-2 px-4 py-2.5 text-left hover:bg-accent/40"
          >
            {changelogOpen ? (
              <ChevronDown className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
            ) : (
              <ChevronRight className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
            )}
            <span className="text-[12px] font-medium">{t(($) => $.qa_cockpit.whats_shipping)}</span>
            <span className="ml-auto text-[11px] text-muted-foreground">
              {t(($) => $.qa_cockpit.whats_shipping_summary, { passed: s.passed, togo })}
            </span>
          </button>
          {changelogOpen ? (
            <div className="space-y-3 px-4 pb-3">
              {/* Detail row — branch + regression chip + verdict-count badges,
                  moved out of the resting headline (detail, not the answer). */}
              <div className="flex flex-wrap items-center gap-2 text-[12px]">
                {s.branch ? (
                  <span className="flex items-center gap-1 text-[11px] text-muted-foreground">
                    <GitBranch className="size-3" aria-hidden /> {s.branch}
                  </span>
                ) : null}
                <RegressionGate gate={s.regression} issueHref={qaDetail} />
                <Badge
                  variant="outline"
                  className="gap-1 text-emerald-600 dark:text-emerald-400"
                  title={t(($) => $.qa_cockpit.sprint_passed_title)}
                >
                  {verdictIcon("pass", "size-3")} {s.passed}
                </Badge>
                {s.failed > 0 ? (
                  <Badge variant="destructive" className="gap-1" title={t(($) => $.qa_cockpit.sprint_failing_title)}>
                    {verdictIcon("fail", "size-3")} {s.failed}
                  </Badge>
                ) : null}
                {s.pending > 0 ? (
                  <Badge
                    variant="outline"
                    className="gap-1 text-muted-foreground"
                    title={t(($) => $.qa_cockpit.sprint_pending_title)}
                  >
                    {verdictIcon("pending", "size-3")} {s.pending}
                  </Badge>
                ) : null}
              </div>
              {changelogGroups.map((g) => (
                <div key={g.key}>
                  <div className="mb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                    {g.label} · {g.items.length}
                  </div>
                  <ul className="space-y-0.5">
                    {g.items.map((i) => (
                      <li key={i.id} className="flex items-center gap-2 text-[12px]">
                        {verdictIcon(i.verdict, "size-3.5 shrink-0")}
                        <AppLink
                          href={qaDetail(i.id)}
                          className="shrink-0 font-medium text-muted-foreground hover:text-foreground"
                        >
                          #{i.number}
                        </AppLink>
                        <span className="min-w-0 truncate">{i.title}</span>
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            </div>
          ) : null}
        </div>
      ) : (
        <div className="border-t px-4 py-3 text-[12px] text-muted-foreground">{t(($) => $.qa_cockpit.sprint_no_tasks)}</div>
      )}

      {/* Deploy is a SPRINT-level cycle (the shared branch ships as a unit) —
          this panel is its home after leaving the per-issue stepper (deploy
          cycle rehome, part 2). Collapsed by default: only a ready sprint whose
          "Ship it" CTA has been pressed reveals it, so a not-ready sprint shows
          zero deploy UI and no card opens with a full panel in it. */}
      {s.mergeable && deployOpen ? (
        <div ref={deployRef}>
          <SprintDeployPanel
            wsId={wsId}
            projectId={s.project_id}
            sprintId={s.sprint_id}
            branch={s.branch}
            issues={s.issues}
          />
        </div>
      ) : null}
    </Card>
  );
}
