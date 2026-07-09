"use client";

import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@agora/core";
import { issueDetailOptions, deployEventsOptions } from "@agora/core/issues/queries";
import { remoteBoxesOptions } from "@agora/core/runtimes";
import { useConfigStore } from "@agora/core/config";
import type { DeployEvent } from "@agora/core/api/schemas";
import { PropRow } from "../../common/prop-row";
import { useT, useTimeAgo } from "../../i18n";
import { verdictIcon } from "../../qa/components/verdict";
import { EditorDeployQA } from "./editor-deploy-qa";

// The Deploy lens — a thin re-mount of EditorDeployQA (the "check this
// branch out onto the project's bound QA box" action) plus a summary of
// which box is bound and its last-known sync state
// (docs/sdlc-stage-cockpit-plan.md, phase E). EditorDeployQA has no Dialog/
// editor-context dependency — it's a standalone component driven entirely by
// its own hooks (remoteBoxesOptions, useDeployIssueQA, useConfigStore) — so
// it mounts here unmodified, same props issue-detail's editor section would
// supply (issueId/wsId/projectId).
//
// A "Recent deploys" section reads the durable deploy_event history (deploy
// P0, docs/deploy-stage-research.md §3.3) so this lens finally shows a
// verdict + history instead of just box metadata — the gap the research doc
// called out (deploy-lens.tsx §1.3: "no verdict, no history").

// One row: ref + status icon + relative time, mirroring the QA verdict
// icon/tint vocabulary (verdictIcon) so success/failure reads consistently
// across the QA and Deploy lenses. Deploy statuses ("success"/"failed") map
// onto the "pass"/"fail" vocabulary verdictIcon expects.
function DeployEventRow({ event }: { event: DeployEvent }) {
  const { t } = useT("issues");
  const timeAgo = useTimeAgo();
  const ok = event.status === "success";
  return (
    <div className="flex min-h-8 items-center gap-2 rounded-md px-2 py-1 text-xs">
      {verdictIcon(ok ? "pass" : "fail", "size-3.5 shrink-0")}
      <span className="min-w-0 flex-1 truncate font-mono text-[11px]">{event.ref || "—"}</span>
      <span className="shrink-0 text-muted-foreground">
        {ok ? t(($) => $.deploy_lens.status_success) : t(($) => $.deploy_lens.status_failed)}
      </span>
      <span className="shrink-0 text-muted-foreground">
        {event.captured_at ? timeAgo(event.captured_at) : "—"}
      </span>
    </div>
  );
}

export function DeployLensBody({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceId();
  const { t } = useT("issues");
  const remoteBoxesEnabled = useConfigStore((s) => s.remoteBoxesEnabled);

  const { data: issue, isLoading } = useQuery(issueDetailOptions(wsId, issueId));
  const { data: boxes = [] } = useQuery({
    ...remoteBoxesOptions(wsId),
    enabled: remoteBoxesEnabled,
  });

  const boundBox = issue?.project_id
    ? (boxes.find((b) => b.project_id === issue.project_id) ?? null)
    : null;

  // Only queried once a box is actually bound — mirrors use-stage-pipeline's
  // hasDeployTarget gate so this lens never fires a useless fetch for an
  // issue with no deploy target.
  const { data: deployEvents } = useQuery({
    ...deployEventsOptions(issueId),
    enabled: !!boundBox,
  });
  const recentDeploys = deployEvents?.recent ?? [];

  if (isLoading || !issue) {
    return (
      <div className="mx-auto w-full max-w-4xl px-8 py-8">
        <p className="text-sm text-muted-foreground">{t(($) => $.timeline.loading)}</p>
      </div>
    );
  }

  if (!remoteBoxesEnabled || !boundBox) {
    return (
      <div className="mx-auto w-full max-w-4xl px-8 py-8">
        <div className="rounded-lg border border-dashed bg-muted/20 px-3 py-5 text-center">
          <p className="text-[12px] text-muted-foreground">{t(($) => $.deploy_lens.empty)}</p>
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-4xl px-8 py-8">
      <div className="[&>*+*]:mt-8 [&>*+*]:border-t [&>*+*]:pt-8">
        <section>
          <div className="mb-2 text-[11px] uppercase tracking-wide text-muted-foreground">
            {t(($) => $.deploy_lens.box_heading)}
          </div>
          <div className="grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5 rounded-lg border px-2 py-1">
            <PropRow label={t(($) => $.deploy_lens.box_label)} interactive={false}>
              {boundBox.label}
            </PropRow>
            <PropRow label={t(($) => $.deploy_lens.box_host)} interactive={false}>
              <span className="font-mono text-[11px]">{boundBox.ssh_host}</span>
            </PropRow>
            <PropRow label={t(($) => $.deploy_lens.box_status)} interactive={false}>
              {boundBox.status}
            </PropRow>
            <PropRow label={t(($) => $.deploy_lens.box_last_branch)} interactive={false}>
              <span className="font-mono text-[11px]">{boundBox.last_branch || "—"}</span>
            </PropRow>
          </div>
        </section>

        <section>
          <div className="mb-2 text-[11px] uppercase tracking-wide text-muted-foreground">
            {t(($) => $.deploy_lens.deploy_heading)}
          </div>
          <EditorDeployQA issueId={issueId} wsId={wsId} projectId={issue.project_id} />
        </section>

        <section>
          <div className="mb-2 text-[11px] uppercase tracking-wide text-muted-foreground">
            {t(($) => $.deploy_lens.history_heading)}
          </div>
          {recentDeploys.length === 0 ? (
            <div className="rounded-lg border border-dashed bg-muted/20 px-3 py-5 text-center">
              <p className="text-[12px] text-muted-foreground">
                {t(($) => $.deploy_lens.history_empty)}
              </p>
            </div>
          ) : (
            <div className="rounded-lg border px-1 py-0.5">
              {recentDeploys.map((event) => (
                <DeployEventRow key={event.id} event={event} />
              ))}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}
