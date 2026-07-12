"use client";

// Assembles `StagePipelineInput` from queries issue-detail (and its
// sibling sections) already fetch, then derives the SDLC stepper's
// pipeline. No new endpoints — every signal here is read from an existing,
// already-cached query, sharing cache entries where the query key matches
// an existing consumer. See docs/sdlc-stage-cockpit-plan.md section 2 and
// phase C.
//
// Deploy is NOT assembled here — it left the issue-level pipeline (stage-
// cockpit rehome, part 1) and now lives at the sprint level
// (qa-sprint-readiness-view.tsx, docs/deploy-mcp-integration.md). This hook
// used to also query remote boxes + deploy_event for a 5th "deploy" stage;
// that query set was removed along with the stage.
//
// Design is NOT assembled here either — it left the stepper as its own
// stage (see packages/core/issues/stage.ts); this hook no longer needs
// qa_evidence's design verdict or a figma-refs check, so that query was
// dropped along with the design fields.

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@agora/core/api";
import { agentTaskSnapshotOptions } from "@agora/core/agents";
import { issueDetailOptions } from "@agora/core/issues/queries";
import { getWorkMode } from "@agora/core/issues/work-mode";
import { issueLabelsOptions } from "@agora/core/labels";
import { issuePullRequestsOptions } from "@agora/core/github";
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

  return useMemo(() => {
    const runningTaskStages: SDLCStage[] = snapshot
      .filter((task) => task.issue_id === issueId && task.status === "running")
      .map((task) => (qaAgentIds?.has(task.agent_id) ? "qa" : "dev"));

    const prNumber = typeof issue?.metadata.pr_number === "number" ? issue.metadata.pr_number : null;
    const matchedPr =
      prNumber != null ? (prs?.pull_requests ?? []).find((pr) => pr.number === prNumber) : undefined;

    return deriveStagePipeline({
      status,
      labels,
      workMode: getWorkMode(issue),
      prNumber,
      mergeGates: mergeReadiness
        ? {
            ci: gateStatus(mergeReadiness.gates, "ci"),
            qa: gateStatus(mergeReadiness.gates, "qa"),
            tier: mergeReadiness.tier,
          }
        : null,
      prMerged: matchedPr ? matchedPr.state === "merged" : undefined,
      runningTaskStages,
    });
  }, [status, labels, issue, prs, mergeReadiness, snapshot, qaAgentIds, issueId]);
}
