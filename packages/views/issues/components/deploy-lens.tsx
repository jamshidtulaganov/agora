"use client";

import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@agora/core";
import { issueDetailOptions } from "@agora/core/issues/queries";
import { remoteBoxesOptions } from "@agora/core/runtimes";
import { useConfigStore } from "@agora/core/config";
import { PropRow } from "../../common/prop-row";
import { useT } from "../../i18n";
import { EditorDeployQA } from "./editor-deploy-qa";

// The Deploy lens — a thin re-mount of EditorDeployQA (the "check this
// branch out onto the project's bound QA box" action) plus a summary of
// which box is bound and its last-known sync state
// (docs/sdlc-stage-cockpit-plan.md, phase E). EditorDeployQA has no Dialog/
// editor-context dependency — it's a standalone component driven entirely by
// its own hooks (remoteBoxesOptions, useDeployIssueQA, useConfigStore) — so
// it mounts here unmodified, same props issue-detail's editor section would
// supply (issueId/wsId/projectId).

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
      </div>
    </div>
  );
}
