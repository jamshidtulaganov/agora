"use client";

// Assembles `StagePipelineInput` from queries issue-detail (and its
// sibling sections) already fetch, then derives the SDLC stepper's
// pipeline. No new endpoints — every signal here is read from an existing,
// already-cached query, sharing cache entries where the query key matches
// an existing consumer. See docs/sdlc-stage-cockpit-plan.md section 2 and
// phase C.

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@agora/core/api";
import { useConfigStore } from "@agora/core/config";
import { remoteBoxesOptions } from "@agora/core/runtimes";
import { agentTaskSnapshotOptions } from "@agora/core/agents";
import { issueDetailOptions, qaEvidenceOptions, deployEventsOptions } from "@agora/core/issues/queries";
import { getWorkMode } from "@agora/core/issues/work-mode";
import { issueLabelsOptions } from "@agora/core/labels";
import { issuePullRequestsOptions } from "@agora/core/github";
import { figmaRefsFrom } from "@agora/core/figma";
import { deriveStagePipeline, type SDLCStage, type StagePipeline, type MergeGateState } from "@agora/core/issues";
import type { MergeGateStatus } from "@agora/core/types";

// Mirrors qa-live-progress.tsx:88-110 exactly (QA-squad member -> "qa",
// everything else defaults to "dev" — the accepted v1 model gap called out
// in the plan). Same queryKey, so the two components share one cache entry
// instead of double-fetching squads + members when both are mounted.
function qaSquadAgentIdsOptions(wsId: string) {
  return {
    queryKey: ["qa-squad-agent-ids", wsId] as const,
    queryFn: async () => {
      const ids = new Set<string>();
      const squads = await api.listSquads();
      for (const s of squads) {
        if (!s.name?.toLowerCase().includes("qa")) continue;
        if (s.leader_id) ids.add(s.leader_id);
        try {
          for (const m of await api.listSquadMembers(s.id)) {
            if (m.member_type === "agent") ids.add(m.member_id);
          }
        } catch {
          /* best-effort */
        }
      }
      return ids;
    },
    enabled: !!wsId,
    staleTime: 300_000,
  };
}

function gateStatus(gates: MergeGateStatus[], name: string): MergeGateState {
  return gates.find((g) => g.name === name)?.status ?? "pending";
}

export function useStagePipeline(wsId: string, issueId: string): StagePipeline {
  const { data: issue } = useQuery(issueDetailOptions(wsId, issueId));
  const { data: labels = [] } = useQuery(issueLabelsOptions(wsId, issueId));
  const { data: evidence } = useQuery(qaEvidenceOptions(issueId));
  const { data: prs } = useQuery(issuePullRequestsOptions(issueId));

  const status = issue?.status ?? "todo";

  // Same queryKey as EditorGates (editor-gates.tsx) — shares the cache when
  // both are mounted on the same issue. Poll only while the review stage is
  // still moving (in_review); once it isn't, the gates are effectively
  // frozen and polling would just churn the network for no signal change.
  const { data: mergeReadiness } = useQuery({
    queryKey: ["merge-readiness", issueId],
    queryFn: () => api.mergeReadiness(issueId),
    enabled: !!issueId,
    refetchInterval: status === "in_review" ? 15000 : undefined,
  });

  const { data: snapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));
  const { data: qaAgentIds } = useQuery(qaSquadAgentIdsOptions(wsId));

  const remoteBoxesEnabled = useConfigStore((s) => s.remoteBoxesEnabled);
  const { data: boxes = [] } = useQuery({
    ...remoteBoxesOptions(wsId),
    enabled: remoteBoxesEnabled,
  });

  // Resolved ahead of the deploy-events query below so its `enabled` gate can
  // reference it — hooks are called unconditionally, so hasDeployTarget can't
  // live inside the final useMemo alongside the values that depend on it.
  const boundBox = issue?.project_id ? boxes.find((b) => b.project_id === issue.project_id) : undefined;
  const hasDeployTarget = remoteBoxesEnabled && !!boundBox;

  // Deploy P0 (docs/deploy-stage-research.md §3.3): the durable deploy_event
  // signal, only queried when a box is actually bound (an issue with no
  // deploy target has nothing to look up).
  const { data: deployEvents } = useQuery({
    ...deployEventsOptions(issueId),
    enabled: hasDeployTarget,
  });

  return useMemo(() => {
    const runningTaskStages: SDLCStage[] = snapshot
      .filter((task) => task.issue_id === issueId && task.status === "running")
      .map((task) => (qaAgentIds?.has(task.agent_id) ? "qa" : "dev"));

    const prNumber = typeof issue?.metadata.pr_number === "number" ? issue.metadata.pr_number : null;
    const matchedPr =
      prNumber != null ? (prs?.pull_requests ?? []).find((pr) => pr.number === prNumber) : undefined;

    const designResult = evidence?.result?.design ?? null;
    const hasDesignSignals =
      figmaRefsFrom(issue?.description ?? "").length > 0 || designResult != null;

    // deploySynced: prefer the durable deploy_event signal (deploy P0,
    // docs/deploy-stage-research.md §3.3) when one has been recorded — success
    // on this issue's box means synced, no ref-matching needed (the server
    // writes the row from the exact deploy-qa call that just ran). Fall back
    // to the pre-table heuristic (box.last_branch === the issue's PR branch)
    // only when no deploy_event exists yet — e.g. an issue whose last sync
    // predates this migration. Known approximation of the FALLBACK path only:
    // ConnectedBox.last_branch is a branch NAME, not a pinned SHA, so a
    // force-push after the last sync still reads as synced until the next
    // deploy-qa run re-syncs it; the deploy_event path doesn't have this gap
    // since every sync attempt writes its own row.
    const latestDeployEvent = deployEvents?.latest ?? null;
    const legacyDeploySynced =
      boundBox?.status === "online" && !!matchedPr?.branch && boundBox.last_branch === matchedPr.branch;
    const deploySynced = latestDeployEvent ? latestDeployEvent.status === "success" : legacyDeploySynced;
    const deployDetail = latestDeployEvent?.ref || undefined;

    return deriveStagePipeline({
      status,
      labels,
      workMode: getWorkMode(issue),
      prNumber,
      hasDesignSignals,
      designVerdict:
        designResult?.verdict === "pass" || designResult?.verdict === "fail" ? designResult.verdict : null,
      qaVerdict: evidence?.verdict === "pass" || evidence?.verdict === "fail" ? evidence.verdict : null,
      mergeGates: mergeReadiness
        ? {
            ci: gateStatus(mergeReadiness.gates, "ci"),
            qa: gateStatus(mergeReadiness.gates, "qa"),
            tier: mergeReadiness.tier,
          }
        : null,
      prMerged: matchedPr ? matchedPr.state === "merged" : undefined,
      runningTaskStages,
      hasDeployTarget,
      deploySynced,
      deployDetail,
    });
  }, [
    status,
    labels,
    issue,
    evidence,
    prs,
    mergeReadiness,
    snapshot,
    qaAgentIds,
    hasDeployTarget,
    boundBox,
    deployEvents,
    issueId,
  ]);
}
