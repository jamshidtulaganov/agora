"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ActorAvatar } from "@agora/ui/components/common/actor-avatar";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@agora/ui/components/ui/popover";
import {
  SidebarMenuButton,
  SidebarMenuItem,
} from "@agora/ui/components/ui/sidebar";
import { cn } from "@agora/ui/lib/utils";
import { useWorkspaceId } from "@agora/core/hooks";
import { useWorkspacePaths } from "@agora/core/paths";
import { agentTaskSnapshotOptions } from "@agora/core/agents";
import { useActorName } from "@agora/core/workspace/hooks";
import type { AgentTask } from "@agora/core/types";
import { AppLink } from "../navigation";
import { formatDuration } from "../agents/components/agent-activity-hover-content";
import { useT } from "../i18n";

// The one glanceable "is anything running, and where?" signal, mounted in the
// sidebar so it's visible on every route. Every other running indicator in the
// app is scoped to a single issue/board/editor; this is the workspace-wide
// answer. Reads the one shared task snapshot (no extra network) and pulses
// while work is live. Clicking opens the roster of running tasks, each a jump
// straight to the issue where the full progress lives (the Live editor pane /
// QA strip). Idle collapses to a calm muted line so the sidebar stays quiet.
export function WorkspaceRunningIndicator() {
  const { t } = useT("layout");
  const wsId = useWorkspaceId();
  const wp = useWorkspacePaths();
  const { getActorName, getActorInitials, getActorAvatarUrl } = useActorName();
  const { data: snapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));

  const { running, queued } = useMemo(() => {
    const run: AgentTask[] = [];
    const q: AgentTask[] = [];
    for (const task of snapshot) {
      if (task.status === "running") run.push(task);
      else if (task.status === "queued" || task.status === "dispatched") q.push(task);
    }
    return { running: run, queued: q };
  }, [snapshot]);

  const isActive = running.length > 0;

  // Tick once per second so the elapsed timers stay live — but only while
  // something is actually running, so an idle workspace pays nothing.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!isActive) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [isActive]);

  // Longest-running elapsed — the collapsed row's at-a-glance "how long".
  const longestElapsed = useMemo(() => {
    if (!isActive) return "";
    let oldest = Infinity;
    for (const task of running) {
      const from = task.started_at ?? task.dispatched_at ?? task.created_at;
      const ms = new Date(from).getTime();
      if (Number.isFinite(ms) && ms < oldest) oldest = ms;
    }
    return Number.isFinite(oldest) ? formatDuration(new Date(oldest).toISOString(), now) : "";
  }, [running, isActive, now]);

  return (
    <Popover>
      <PopoverTrigger
        render={
          <SidebarMenuButton
            className={cn(
              "text-muted-foreground",
              isActive && "text-foreground hover:not-data-active:bg-sidebar-accent/70",
            )}
            aria-label={
              isActive
                ? t(($) => $.running.active, { count: running.length })
                : t(($) => $.running.idle)
            }
          >
            <RunningDot active={isActive} />
            <span className="flex-1 truncate">
              {isActive
                ? t(($) => $.running.active, { count: running.length })
                : t(($) => $.running.idle)}
            </span>
            {isActive && longestElapsed && (
              <span className="ml-auto shrink-0 tabular-nums text-[11px] text-muted-foreground">
                {longestElapsed}
              </span>
            )}
          </SidebarMenuButton>
        }
      />
      <PopoverContent align="start" side="right" className="w-72 p-2">
        <div className="mb-1.5 px-1 text-xs font-medium text-muted-foreground">
          {t(($) => $.running.header)}
        </div>
        {running.length === 0 && queued.length === 0 ? (
          <p className="px-1 py-2 text-xs text-muted-foreground">
            {t(($) => $.running.empty)}
          </p>
        ) : (
          <div className="flex flex-col gap-0.5">
            {running.map((task) => (
              <TaskRow
                key={task.id}
                task={task}
                href={wp.issueDetail(task.issue_id)}
                elapsed={formatDuration(
                  task.started_at ?? task.dispatched_at ?? task.created_at,
                  now,
                )}
                live
                name={getActorName("agent", task.agent_id)}
                initials={getActorInitials("agent", task.agent_id)}
                avatarUrl={getActorAvatarUrl("agent", task.agent_id)}
              />
            ))}
            {queued.map((task) => (
              <TaskRow
                key={task.id}
                task={task}
                href={wp.issueDetail(task.issue_id)}
                elapsed={t(($) => $.running.queued, { count: 1 })}
                live={false}
                name={getActorName("agent", task.agent_id)}
                initials={getActorInitials("agent", task.agent_id)}
                avatarUrl={getActorAvatarUrl("agent", task.agent_id)}
              />
            ))}
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}

// The pulsing "live" dot — same running idiom as the SDLC stepper (a soft
// expanding ping behind a solid brand dot), gated motion-safe. Idle is a
// hollow ring so the presence/absence of motion is the signal.
function RunningDot({ active }: { active: boolean }) {
  if (!active) {
    return (
      <span
        aria-hidden
        className="size-2 shrink-0 rounded-full border border-muted-foreground/40"
      />
    );
  }
  return (
    <span aria-hidden className="relative inline-flex size-2.5 shrink-0 items-center justify-center">
      <span className="absolute inline-flex size-2.5 rounded-full bg-brand opacity-40 motion-safe:animate-ping" />
      <span className="relative inline-flex size-1.5 rounded-full bg-brand" />
    </span>
  );
}

function TaskRow({
  task,
  href,
  elapsed,
  live,
  name,
  initials,
  avatarUrl,
}: {
  task: AgentTask;
  href: string;
  elapsed: string;
  live: boolean;
  name: string;
  initials: string;
  avatarUrl: string | null;
}) {
  // trigger_summary is the richest one-liner we have on the snapshot ("what
  // it's doing"); fall back to the agent name for direct assignments.
  const label = task.trigger_summary?.trim() || name;
  return (
    <AppLink
      href={href}
      className="flex items-center gap-2 rounded-md px-1.5 py-1.5 text-xs transition-colors hover:bg-accent"
    >
      <ActorAvatar name={name} initials={initials} avatarUrl={avatarUrl} isAgent size={18} />
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate font-medium">{name}</span>
        <span className="truncate text-[11px] text-muted-foreground">{label}</span>
      </span>
      <span
        className={cn(
          "shrink-0 tabular-nums text-[11px]",
          live ? "text-brand" : "text-muted-foreground",
        )}
      >
        {elapsed}
      </span>
    </AppLink>
  );
}

// Sidebar-item wrapper so the caller drops it straight into a SidebarMenu.
export function WorkspaceRunningIndicatorItem() {
  return (
    <SidebarMenuItem>
      <WorkspaceRunningIndicator />
    </SidebarMenuItem>
  );
}
