"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@agora/core/hooks";
import { useActorName } from "@agora/core/workspace/hooks";
import { agentTaskSnapshotOptions } from "@agora/core/agents";
import type { AgentTask } from "@agora/core/types";
import { AgentAvatarStack } from "../../agents/components/agent-avatar-stack";
import { useT } from "../../i18n";

// Chat-app-style "agent is working / typing…" row for the issue comment
// thread. Appears the moment a user comment (or assignment) puts the issue's
// agent into a queued/running task and disappears when the run ends — the
// conversational counterpart to the header `IssueAgentHeaderChip`, placed in
// the content column so that right after you post a reply the wait reads like
// a chat "typing…" indicator instead of silence.
//
// Same single source of truth as the header chip + board/list indicators: the
// workspace-wide agent task snapshot filtered by issue id. WS `task:*` events
// invalidate that snapshot (see useRealtimeSync), so this updates live with no
// polling and stays consistent with every other "agent working" surface.
// Reuses the existing `agent_live` / `agent_activity` i18n strings — no new keys.

interface AgentWorkingIndicatorProps {
  issueId: string;
}

export function AgentWorkingIndicator({ issueId }: AgentWorkingIndicatorProps) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const { getActorName } = useActorName();
  const { data: snapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));

  const { running, queued } = useMemo(() => {
    const running: AgentTask[] = [];
    const queued: AgentTask[] = [];
    for (const task of snapshot) {
      if (task.issue_id !== issueId) continue;
      if (task.status === "running") running.push(task);
      else if (
        task.status === "queued" ||
        task.status === "dispatched" ||
        // Daemon-parked on a busy local_directory — still active (waiting on a
        // path lock), so it belongs in the live row, not dropped.
        task.status === "waiting_local_directory"
      )
        queued.push(task);
      // Terminal statuses are the execution log's story, not the live row's.
    }
    return { running, queued };
  }, [snapshot, issueId]);

  // Nothing in flight → render nothing (no empty banner).
  if (running.length === 0 && queued.length === 0) return null;

  const active = [...running, ...queued];
  const agentIds = [...new Set(active.map((tk) => tk.agent_id))];
  const anyRunning = running.length > 0;
  const isSingle = agentIds.length === 1;

  // Copy follows the real state: "is working" only when something is truly
  // running; queued/dispatched/parked reads "is queued" so a not-yet-started
  // agent isn't mislabelled. Mirrors IssueAgentHeaderChip exactly.
  const label = isSingle
    ? t(($) => (anyRunning ? $.agent_live.is_working : $.agent_live.is_queued), {
        name: getActorName("agent", agentIds[0] ?? ""),
      })
    : t(
        ($) =>
          anyRunning
            ? $.agent_activity.hover_header
            : $.agent_activity.hover_header_queued,
        { count: agentIds.length },
      );

  return (
    <div
      className="flex items-center gap-2 px-1 py-2 text-xs"
      aria-live="polite"
    >
      <AgentAvatarStack
        agentIds={agentIds}
        size={18}
        max={3}
        opacity={anyRunning ? "full" : "half"}
      />
      <span className={anyRunning ? "text-info" : "text-muted-foreground"}>
        {label}
      </span>
      {/* Chat-style "typing…" dots — only while genuinely running, so queued
          state stays calm (matches the header chip reserving motion for live
          work). Three staggered bouncing dots is the universal typing motif. */}
      {anyRunning && (
        <span className="inline-flex items-center gap-0.5" aria-hidden="true">
          <span className="size-1 rounded-full bg-info animate-bounce [animation-delay:-0.3s]" />
          <span className="size-1 rounded-full bg-info animate-bounce [animation-delay:-0.15s]" />
          <span className="size-1 rounded-full bg-info animate-bounce" />
        </span>
      )}
    </div>
  );
}
