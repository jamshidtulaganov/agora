"use client";

import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { CheckCircle2, XCircle, CircleDashed, GitBranch, Rocket, Play, Plus, ShieldCheck, ShieldAlert, Loader2 } from "lucide-react";
import { api } from "@agora/core/api";
import type { SprintReadinessResponse } from "@agora/core/api/schemas";
import { useWorkspacePaths } from "@agora/core/paths";
import { useWorkspaceId } from "@agora/core/hooks";
import { Button } from "@agora/ui/components/ui/button";
import { AppLink } from "../../navigation";
import { IssuePickerModal } from "../../modals/issue-picker-modal";
import { SprintDeployPanel } from "./sprint-deploy-panel";

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
  const { data, isLoading } = useQuery({
    // wsId in the key: the fetch scopes by the ambient workspace header, so
    // without it a workspace switch served the previous workspace's cache.
    queryKey: ["qa-sprint-readiness", wsId, projectId ?? "all"],
    queryFn: () => api.getSprintReadiness(projectId),
    staleTime: 30_000,
    refetchInterval: 60_000,
  });

  const sprints = data?.sprints ?? [];

  if (isLoading && !data) {
    return <div className="px-8 py-6 text-sm text-muted-foreground">Loading sprint readiness…</div>;
  }
  if (sprints.length === 0) {
    return (
      <div className="px-8 py-10 text-center text-sm text-muted-foreground">
        No active sprints. Sprint readiness appears here once a project has an active sprint.
      </div>
    );
  }

  return (
    <div className="flex w-full flex-col gap-6 px-8 py-6">
      <p className="max-w-2xl text-sm text-muted-foreground">
        Is each active sprint mergeable? Every task on the shared sprint branch, by QA verdict —
        the human qa:pass/qa:fail plus the automated regression runs. A sprint is mergeable when
        every task passed and none is failing or pending.
      </p>

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
  if (!gate || !gate.status) {
    return <span className="text-[11px] text-muted-foreground">regression: never run</span>;
  }
  const done = gate.status === "completed" || gate.status === "succeeded";
  const failed = gate.status === "failed" || gate.status === "error";
  const Icon = failed ? ShieldAlert : done ? ShieldCheck : Loader2;
  const cls = failed ? "text-destructive" : done ? "text-emerald-500" : "text-muted-foreground";
  const body = (
    <>
      <Icon className={`size-3.5 ${!done && !failed ? "animate-spin" : ""}`} aria-hidden />
      regression {gate.status}
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
  const qc = useQueryClient();
  const [attachOpen, setAttachOpen] = useState(false);
  const pct = s.total > 0 ? Math.round((s.passed / s.total) * 100) : 0;
  const runRegression = useMutation({
    mutationFn: () => api.runSprintRegression(s.sprint_id),
    onSuccess: () => {
      toast.success("Sprint regression fired");
      void qc.invalidateQueries({ queryKey: ["qa-sprint-readiness"] });
    },
    onError: (e) => toast.error(e instanceof Error && e.message ? e.message : "Failed to run regression"),
  });
  // Attach an existing project task to this sprint so it joins the sprint's
  // regression scope + mergeability rollup. issue_to_sprint is one-sprint-per-
  // issue (PK = issue_id), so attaching an issue that already belongs to a
  // DIFFERENT sprint silently MOVES it — steal it only after an explicit
  // confirm (audit P1: the picker offered such issues with no warning).
  const attachTask = useMutation({
    mutationFn: async (issueId: string) => {
      const current = await api.getIssueSprint(issueId).catch(() => null);
      if (current && current.id && current.id !== s.sprint_id) {
        const move = window.confirm(
          `This task already belongs to sprint "${current.name}". Attaching it here MOVES it out of that sprint (its regression scope and rollup). Move it?`,
        );
        if (!move) throw new Error("cancelled");
      }
      return api.assignIssueSprint(issueId, s.sprint_id);
    },
    onSuccess: () => {
      toast.success("Task attached to sprint");
      void qc.invalidateQueries({ queryKey: ["qa-sprint-readiness"] });
    },
    onError: (e) => toast.error(e instanceof Error && e.message ? e.message : "Failed to attach task"),
  });

  return (
    <div className="rounded-xl border bg-card">
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
          <span className="flex items-center gap-1 text-emerald-500" title="passed">
            <CheckCircle2 className="size-3.5" aria-hidden /> {s.passed}
          </span>
          <span className="flex items-center gap-1 text-destructive" title="failing">
            <XCircle className="size-3.5" aria-hidden /> {s.failed}
          </span>
          <span className="flex items-center gap-1 text-muted-foreground" title={`pending (${s.no_qa} not QA'd yet)`}>
            <CircleDashed className="size-3.5" aria-hidden /> {s.pending}
            {s.no_qa > 0 ? <span className="text-[10px]">({s.no_qa} no-QA)</span> : null}
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
            {s.mergeable ? "Mergeable" : "Not ready"}
          </span>
          <Button
            size="sm"
            variant="outline"
            className="h-7 gap-1 text-[11px]"
            onClick={() => setAttachOpen(true)}
          >
            <Plus className="size-3.5" />
            Attach tasks
          </Button>
          <Button
            size="sm"
            variant="outline"
            className="h-7 gap-1 text-[11px]"
            disabled={runRegression.isPending}
            onClick={() => runRegression.mutate()}
          >
            {runRegression.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Play className="size-3.5" />}
            Run regression
          </Button>
        </div>
      </div>

      <IssuePickerModal
        open={attachOpen}
        onOpenChange={setAttachOpen}
        title="Attach tasks to sprint"
        description={`Add a task from ${s.project_title} to ${s.name} for regression coverage.`}
        projectId={s.project_id}
        excludeIds={s.issues.map((i) => i.id)}
        onSelect={(issue) => attachTask.mutate(issue.id)}
      />

      <div className="h-1 w-full bg-muted">
        <div
          className={s.failed > 0 ? "h-1 bg-destructive" : "h-1 bg-emerald-500"}
          style={{ width: `${pct}%` }}
        />
      </div>

      {s.issues.length === 0 ? (
        <div className="px-4 py-4 text-[12px] text-muted-foreground">No tasks in this sprint.</div>
      ) : (
        <ul className="divide-y">
          {s.issues.map((i) => (
            <li key={i.id} className="flex items-center gap-3 px-4 py-2 text-[12px]">
              <VerdictDot verdict={i.verdict} />
              <AppLink href={qaDetail(i.id)} className="shrink-0 font-medium text-muted-foreground hover:text-foreground">
                #{i.number}
              </AppLink>
              <span className="truncate">{i.title}</span>
              <span className="ml-auto flex shrink-0 items-center gap-2 text-[11px] text-muted-foreground">
                {i.runs_total > 0 ? (
                  <span title="automated regression runs (pass/fail)">
                    {i.runs_fail > 0 ? (
                      <span className="text-destructive">{i.runs_fail} failing</span>
                    ) : (
                      <span className="text-emerald-500">{i.runs_pass} runs</span>
                    )}
                  </span>
                ) : (
                  <span>no runs</span>
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
