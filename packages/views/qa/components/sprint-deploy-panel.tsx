"use client";

// Sprint-level DEPLOY panel — the deploy cycle's home after it left the
// per-issue stepper (deploy cycle rehome, part 2). A sprint ships as ONE
// shared branch, so "deploy" is a property of the sprint, not of any single
// task: this panel lists the project's configured deploy environments
// (project.settings.deploy_environments, MCP-P1 docs/deploy-mcp-integration.md
// §3), fires the `deploy` slice-action per environment with the SPRINT BRANCH
// as the ref, and shows the recent deploy history.
//
// The deploy endpoint is ISSUE-scoped (POST /api/issues/{id}/slice-actions),
// so the dispatch anchors to a deterministic representative issue of the
// sprint — see resolveAnchorIssue below. The deploy_event rows the agent's
// ```deploy-result``` write-back produces land on that same anchor issue,
// which is why the history read (GET /api/issues/{anchor}/deploy-events, the
// endpoint the write path already populates — no new read surface) uses the
// same anchor.

import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Rocket, ShieldAlert, Loader2 } from "lucide-react";
import { api } from "@agora/core/api";
import {
  parseDeployEnvironments,
  deployEnvironmentRequiresHuman,
  type DeployEnvironment,
  type DeployEvent,
} from "@agora/core/api/schemas";
import { projectDetailOptions } from "@agora/core/projects";
import { deployEventsOptions } from "@agora/core/issues/queries";
import { Button } from "@agora/ui/components/ui/button";
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
import { useT, useTimeAgo } from "../../i18n";
import { verdictIcon } from "./verdict";

export interface SprintDeployPanelIssue {
  id: string;
  number: number;
  status: string;
}

export interface SprintDeployPanelProps {
  wsId: string;
  projectId: string;
  sprintId: string;
  /** The sprint's explicit branch ("" when unset — the panel then falls back
   *  to the `sprint/<id>` convention, mirroring SprintBranchFor server-side). */
  branch: string;
  issues: SprintDeployPanelIssue[];
}

// Deterministic representative issue for the ISSUE-scoped deploy endpoint:
// the HIGHEST-NUMBERED NON-CANCELLED issue in the sprint. Issue numbers are
// workspace-monotonic, so this reads as "the sprint's newest still-valid
// task" — derived purely from data the readiness payload already carries (no
// extra fetch), stable for a given sprint composition, and shared by the
// dispatch AND the history read so the deploy_event rows written by one are
// found by the other. Known trade-off: attaching a newer task moves the
// anchor, so history shows the deploys fired since that attach — acceptable
// for a "recent deploys" panel (the full log lives on the issues themselves).
function resolveAnchorIssue(issues: SprintDeployPanelIssue[]): SprintDeployPanelIssue | null {
  let anchor: SprintDeployPanelIssue | null = null;
  for (const issue of issues) {
    if (issue.status === "cancelled") continue;
    if (!anchor || issue.number > anchor.number) anchor = issue;
  }
  return anchor;
}

function DeployHistoryRow({ event }: { event: DeployEvent }) {
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const ok = event.status === "success";
  const statusLabel =
    event.status === "success"
      ? t(($) => $.sprint_deploy.status_success)
      : event.status === "timeout"
        ? t(($) => $.sprint_deploy.status_timeout)
        : t(($) => $.sprint_deploy.status_failed);
  return (
    <div className="flex min-h-7 items-center gap-2 px-2 py-1 text-[11px]">
      {verdictIcon(ok ? "pass" : "fail", "size-3.5 shrink-0")}
      <span className="min-w-0 flex-1 truncate font-mono">{event.ref || "—"}</span>
      {event.target ? (
        <span className="shrink-0 rounded border px-1.5 py-0.5 uppercase tracking-wide text-muted-foreground">
          {event.target}
        </span>
      ) : null}
      <span className="shrink-0 text-muted-foreground">{statusLabel}</span>
      <span className="shrink-0 text-muted-foreground">
        {event.captured_at ? timeAgo(event.captured_at) : "—"}
      </span>
    </div>
  );
}

export function SprintDeployPanel({ wsId, projectId, sprintId, branch, issues }: SprintDeployPanelProps) {
  const { t } = useT("issues");
  const qc = useQueryClient();

  // The ref every deploy fires with: the sprint's explicit branch, or the
  // sprint/<id> convention — mirrors SprintBranchFor (connected_box.go) so
  // the panel and the backend's own sprint deploys name the same branch.
  const sprintBranch = branch.trim() !== "" ? branch.trim() : `sprint/${sprintId}`;

  const anchor = useMemo(() => resolveAnchorIssue(issues), [issues]);

  const { data: project } = useQuery(projectDetailOptions(wsId, projectId));
  // Defensive parse (parseWithFallback discipline): a malformed settings blob
  // or entry yields []/skips — the panel then shows the empty state instead
  // of crashing (schemas.ts parseDeployEnvironments, mirrors the server).
  const environments = useMemo(
    () => parseDeployEnvironments(project?.settings),
    [project?.settings],
  );

  // History only makes sense once there's an anchor to have written it; the
  // envs gate avoids a useless fetch for projects with no deploy config.
  const { data: deployEvents } = useQuery({
    ...deployEventsOptions(anchor?.id ?? ""),
    enabled: !!anchor && environments.length > 0,
    refetchInterval: 60_000,
  });
  const recentDeploys = deployEvents?.recent ?? [];

  const deploy = useMutation({
    mutationFn: (env: DeployEnvironment) =>
      api.sliceAction(anchor!.id, { kind: "deploy", scope: env.key, ref: sprintBranch }),
    onSuccess: (_data, env) => {
      toast.success(t(($) => $.sprint_deploy.toast_fired, { env: env.label || env.key }));
      if (anchor) {
        void qc.invalidateQueries({ queryKey: deployEventsOptions(anchor.id).queryKey });
      }
    },
    onError: (e) =>
      toast.error(
        e instanceof Error && e.message ? e.message : t(($) => $.sprint_deploy.toast_failed),
      ),
  });

  // The server 403s machine actors for requires_human/production environments
  // regardless (resolveDeployEnvironment) — this confirm is the HUMAN-side
  // speed bump for the human-only trigger. Set only while the confirm dialog
  // for a human-gated environment is open.
  const [pendingEnv, setPendingEnv] = useState<DeployEnvironment | null>(null);

  const fire = (env: DeployEnvironment) => {
    if (!anchor) return;
    if (deployEnvironmentRequiresHuman(env)) {
      setPendingEnv(env);
      return;
    }
    deploy.mutate(env);
  };

  return (
    <div data-testid="sprint-deploy-panel" className="border-t px-4 py-3">
      <div className="mb-2 flex items-center gap-1.5 text-[11px] uppercase tracking-wide text-muted-foreground">
        <Rocket className="size-3" aria-hidden />
        {t(($) => $.sprint_deploy.heading)}
        <span className="font-mono normal-case tracking-normal">{sprintBranch}</span>
      </div>

      {environments.length === 0 ? (
        <p className="text-[12px] text-muted-foreground">{t(($) => $.sprint_deploy.empty)}</p>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2">
            {environments.map((env) => {
              const humanGated = deployEnvironmentRequiresHuman(env);
              return (
                <div key={env.key} className="flex items-center gap-1.5 rounded-md border px-2 py-1">
                  <span className="text-[12px] font-medium">{env.label || env.key}</span>
                  {humanGated ? (
                    <ShieldAlert
                      className="size-3.5 text-amber-600 dark:text-amber-400"
                      aria-label={t(($) => $.sprint_deploy.requires_human)}
                    />
                  ) : null}
                  <Button
                    size="sm"
                    variant="outline"
                    className="h-6 gap-1 text-[11px]"
                    data-testid={`sprint-deploy-${env.key}`}
                    disabled={!anchor || deploy.isPending}
                    onClick={() => fire(env)}
                  >
                    {deploy.isPending ? (
                      <Loader2 className="size-3 animate-spin" aria-hidden />
                    ) : (
                      <Rocket className="size-3" aria-hidden />
                    )}
                    {t(($) => $.sprint_deploy.deploy)}
                  </Button>
                </div>
              );
            })}
          </div>
          {!anchor ? (
            <p className="mt-2 text-[11px] text-muted-foreground">
              {t(($) => $.sprint_deploy.no_anchor)}
            </p>
          ) : null}

          <div className="mt-3">
            <div className="mb-1 text-[11px] uppercase tracking-wide text-muted-foreground">
              {t(($) => $.sprint_deploy.history_heading)}
            </div>
            {recentDeploys.length === 0 ? (
              <p className="text-[11px] text-muted-foreground">
                {t(($) => $.sprint_deploy.history_empty)}
              </p>
            ) : (
              <div className="divide-y rounded-md border">
                {recentDeploys.map((event) => (
                  <DeployHistoryRow key={event.id} event={event} />
                ))}
              </div>
            )}
          </div>
        </>
      )}

      <AlertDialog open={!!pendingEnv} onOpenChange={(open) => !open && setPendingEnv(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.sprint_deploy.confirm_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {pendingEnv &&
                t(($) => $.sprint_deploy.confirm, { branch: sprintBranch, env: pendingEnv.label || pendingEnv.key })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.test_cases.cancel)}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (pendingEnv) deploy.mutate(pendingEnv);
                setPendingEnv(null);
              }}
            >
              {t(($) => $.sprint_deploy.deploy)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
