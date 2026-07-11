"use client";

import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { CheckCircle2, XCircle, CircleDashed, GitBranch, Rocket, Play, Plus, Loader2 } from "lucide-react";
import { api } from "@agora/core/api";
import { sprintReadinessOptions } from "@agora/core/qa/queries";
import type { SprintReadinessResponse } from "@agora/core/api/schemas";
import { useWorkspacePaths } from "@agora/core/paths";
import { useWorkspaceId } from "@agora/core/hooks";
import { Button } from "@agora/ui/components/ui/button";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
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
import { useT } from "../../i18n";
import { AppLink } from "../../navigation";
import { IssuePickerModal } from "../../modals/issue-picker-modal";
import { SprintDeployPanel } from "./sprint-deploy-panel";
import { regressionStatusMeta } from "./regression-status";

// Sprint QA-readiness — "is this sprint mergeable?" Per active sprint: the issue
// rows by QA verdict (human qa:pass/qa:fail + automated regression runs) and a
// green/blocked rollup. Answers, at a glance, whether the shared sprint branch
// is safe to merge to base. Reads GET /api/qa/sprint-readiness.

function VerdictDot({ verdict }: { verdict: string }) {
  if (verdict === "pass") return <CheckCircle2 className="size-4 shrink-0 text-emerald-500" aria-label="pass" />;
  if (verdict === "fail") return <XCircle className="size-4 shrink-0 text-destructive" aria-label="fail" />;
  return <CircleDashed className="size-4 shrink-0 text-muted-foreground" aria-label="pending" />;
}

export function QASprintReadinessView({ projectId }: { projectId?: string }) {
  const wp = useWorkspacePaths();
  const wsId = useWorkspaceId();
  const { t } = useT("issues");
  // Shared factory — the same cache entry the health strip reads.
  const { data, isLoading } = useQuery(sprintReadinessOptions(wsId, projectId));

  const sprints = data?.sprints ?? [];

  if (isLoading && !data) {
    return (
      <div className="w-full space-y-2 px-8 py-8" aria-hidden>
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }
  if (sprints.length === 0) {
    return (
      <div className="w-full px-8 py-10 text-center text-sm text-muted-foreground">
        {t(($) => $.qa_cockpit.sprint_empty)}
      </div>
    );
  }

  return (
    <div className="flex w-full flex-col gap-6 px-8 py-8">
      <p className="text-sm text-muted-foreground">{t(($) => $.qa_cockpit.sprint_description)}</p>

      {/* The QA lens lives inside the issue cockpit now (docs/sdlc-stage-cockpit-plan.md
          phase D) — there is no more dedicated /qa/<id> page, so these links
          deep-link to the issue with the QA stage pre-selected. */}
      {sprints.map((s) => (
        <SprintCard key={s.sprint_id} sprint={s} wsId={wsId} qaDetail={(id) => `${wp.issueDetail(id)}?lens=qa`} />
      ))}
    </div>
  );
}

type SprintData = SprintReadinessResponse["sprints"][number];

function RegressionGate({ gate, issueHref }: { gate: SprintData["regression"]; issueHref: (id: string) => string }) {
  const { t } = useT("issues");
  if (!gate || !gate.status) {
    return <span className="text-[11px] text-muted-foreground">{t(($) => $.qa_cockpit.sprint_regression_never_run)}</span>;
  }
  // Shared status classification (regression-status.ts) — the health strip's
  // glyph renders the same mapping, so the two can't drift.
  const { running, Icon, className: cls } = regressionStatusMeta(gate.status);
  const body = (
    <>
      <Icon className={`size-3.5 ${running ? "animate-spin" : ""}`} aria-hidden />
      {t(($) => $.qa_cockpit.sprint_regression_status, { status: gate.status })}
    </>
  );
  // Click-through to the run's tracking issue — the chip used to be a
  // dead-end (toast + tooltip only) with no way to reach the agent output.
  if (gate.run_issue_id) {
    return (
      <AppLink
        href={issueHref(gate.run_issue_id)}
        className={`flex items-center gap-1 text-[11px] underline-offset-2 hover:underline ${cls}`}
        title={gate.reason || gate.status}
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

function SprintCard({ sprint: s, wsId, qaDetail }: { sprint: SprintData; wsId: string; qaDetail: (id: string) => string }) {
  const { t } = useT("issues");
  const qc = useQueryClient();
  const [attachOpen, setAttachOpen] = useState(false);
  // Set only when attaching an issue that already belongs to a DIFFERENT
  // sprint — issue_to_sprint is one-sprint-per-issue (PK = issue_id), so
  // attaching such an issue silently MOVES it. Gated behind an explicit
  // confirm dialog (audit P1: the picker used to offer such issues with no
  // warning at all).
  const [pendingMove, setPendingMove] = useState<{ issueId: string; sprintName: string } | null>(null);
  const pct = s.total > 0 ? Math.round((s.passed / s.total) * 100) : 0;
  // Fail-first row order: the rows blocking the merge surface at the top of
  // the card (pending next, passed last); stable sort keeps server order
  // within each group.
  const verdictRank = (v: string) => (v === "fail" ? 0 : v === "pass" ? 2 : 1);
  const sortedIssues = [...s.issues].sort((a, b) => verdictRank(a.verdict) - verdictRank(b.verdict));
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

  return (
    <div className="rounded-lg border bg-card">
      <div className="flex flex-wrap items-center gap-3 border-b px-4 py-3">
        <div className="flex min-w-0 flex-col">
          <span className="truncate text-[13px] font-semibold">
            {s.project_title} · {s.name}
          </span>
          {s.branch ? (
            <span className="flex items-center gap-1 text-[11px] text-muted-foreground">
              <GitBranch className="size-3" aria-hidden /> {s.branch}
            </span>
          ) : null}
        </div>

        <div className="ml-auto flex items-center gap-3 text-[12px]">
          <RegressionGate gate={s.regression} issueHref={qaDetail} />
          <span className="flex items-center gap-1 text-emerald-500" title={t(($) => $.qa_cockpit.sprint_passed_title)}>
            <CheckCircle2 className="size-3.5" aria-hidden /> {s.passed}
          </span>
          <span className="flex items-center gap-1 text-destructive" title={t(($) => $.qa_cockpit.sprint_failing_title)}>
            <XCircle className="size-3.5" aria-hidden /> {s.failed}
          </span>
          <span
            className="flex items-center gap-1 text-muted-foreground"
            title={t(($) => $.qa_cockpit.sprint_pending_title, { count: s.no_qa })}
          >
            <CircleDashed className="size-3.5" aria-hidden /> {s.pending}
            {s.no_qa > 0 ? <span className="text-[10px]">{t(($) => $.qa_cockpit.sprint_no_qa_hint, { count: s.no_qa })}</span> : null}
          </span>
          <span className="text-muted-foreground">/ {s.total}</span>
          <span
            className={
              s.mergeable
                ? "flex items-center gap-1 rounded-full bg-emerald-500/15 px-2.5 py-1 text-[11px] font-medium text-emerald-500"
                : "flex items-center gap-1 rounded-full bg-muted px-2.5 py-1 text-[11px] font-medium text-muted-foreground"
            }
          >
            <Rocket className="size-3.5" aria-hidden />
            {s.mergeable ? t(($) => $.qa_cockpit.sprint_mergeable) : t(($) => $.qa_cockpit.sprint_not_ready)}
          </span>
          <Button
            size="sm"
            variant="outline"
            className="h-7 gap-1 text-[11px]"
            onClick={() => setAttachOpen(true)}
          >
            <Plus className="size-3.5" />
            {t(($) => $.qa_cockpit.sprint_attach_tasks)}
          </Button>
          <Button
            size="sm"
            variant="outline"
            className="h-7 gap-1 text-[11px]"
            disabled={runRegression.isPending}
            onClick={() => runRegression.mutate()}
          >
            {runRegression.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Play className="size-3.5" />}
            {t(($) => $.qa_cockpit.sprint_run_regression)}
          </Button>
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

      <div className="h-1 w-full bg-muted">
        <div
          className={s.failed > 0 ? "h-1 bg-destructive" : "h-1 bg-emerald-500"}
          style={{ width: `${pct}%` }}
        />
      </div>

      {s.issues.length === 0 ? (
        <div className="px-4 py-4 text-[12px] text-muted-foreground">{t(($) => $.qa_cockpit.sprint_no_tasks)}</div>
      ) : (
        <ul className="divide-y">
          {sortedIssues.map((i) => (
            <li key={i.id} className="flex items-center gap-3 px-4 py-2 text-[12px]">
              <VerdictDot verdict={i.verdict} />
              <AppLink href={qaDetail(i.id)} className="shrink-0 font-medium text-muted-foreground hover:text-foreground">
                #{i.number}
              </AppLink>
              <span className="truncate">{i.title}</span>
              <span className="ml-auto flex shrink-0 items-center gap-2 text-[11px] text-muted-foreground">
                {i.runs_total > 0 ? (
                  <span title={t(($) => $.qa_cockpit.sprint_runs_tooltip)}>
                    {i.runs_fail > 0 ? (
                      <span className="text-destructive">{t(($) => $.qa_cockpit.sprint_runs_failing, { count: i.runs_fail })}</span>
                    ) : (
                      <span className="text-emerald-500">{t(($) => $.qa_cockpit.sprint_runs_count, { count: i.runs_pass })}</span>
                    )}
                  </span>
                ) : (
                  <span>{t(($) => $.qa_cockpit.sprint_no_runs)}</span>
                )}
                <span className="rounded border px-1.5 py-0.5 uppercase tracking-wide">{i.status}</span>
              </span>
            </li>
          ))}
        </ul>
      )}

      {/* Deploy is a SPRINT-level cycle (the shared branch ships as a unit) —
          this panel is its home after leaving the per-issue stepper (deploy
          cycle rehome, part 2). */}
      <SprintDeployPanel
        wsId={wsId}
        projectId={s.project_id}
        sprintId={s.sprint_id}
        branch={s.branch}
        issues={s.issues}
      />
    </div>
  );
}
