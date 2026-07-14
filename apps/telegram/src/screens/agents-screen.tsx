import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Bot, MessageCircle } from "lucide-react";
import { agentListOptions, squadListOptions } from "@agora/core/workspace/queries";
import { agentTaskSnapshotOptions } from "@agora/core/agents";
import { useWorkspacePresenceMap } from "@agora/core/agents/use-agent-presence";
import type { AgentPresenceDetail } from "@agora/core/agents";
import { issueListOptions } from "@agora/core/issues/queries";
import { chatSessionsOptions } from "@agora/core/chat/queries";
import { useCreateChatSession } from "@agora/core/chat/mutations";
import { useWorkspaceId } from "@agora/core/hooks";
import type { Agent, AgentTask } from "@agora/core/types";
import { useRouter } from "../platform/navigation";
import { AgentAvatar, AgentTag, type AgentStatusTone } from "../components/agent-avatar";
import { CenterMessage } from "../components/center-message";
import { TabSkeleton } from "../components/skeleton";
import { QueryError } from "../components/query-error";
import { haptic } from "../telegram/sdk";
import { useT, useFormatRelative } from "../i18n";
import { cn } from "../lib/cn";

// Agents tab (design 5a §2.4): one card per active agent with live presence
// (running / queued / idle / offline), the issue it is working on, and a
// chat shortcut. No pause button — the API has no pause endpoint.

// Rendered while the presence map hasn't resolved yet: plain idle, no dot
// noise. Distinct from OFFLINE_DETAIL so a slow presence fetch doesn't
// flash every agent as offline.
const LOADING_DETAIL: AgentPresenceDetail = {
  availability: "online",
  workload: "idle",
  runningCount: 0,
  queuedCount: 0,
  capacity: 0,
};

// Presence resolved but this agent is missing from the map — treat as offline.
const OFFLINE_DETAIL: AgentPresenceDetail = {
  availability: "offline",
  workload: "idle",
  runningCount: 0,
  queuedCount: 0,
  capacity: 0,
};

function avatarStatus(d: AgentPresenceDetail): AgentStatusTone {
  if (d.workload === "working") return "running";
  switch (d.availability) {
    case "offline":
    case "archived":
      return null;
    case "unstable":
      return "paused";
    default:
      return "idle";
  }
}

function presenceRank(d: AgentPresenceDetail): number {
  if (d.workload === "working") return 0;
  if (d.queuedCount > 0) return 1;
  if (d.availability === "offline" || d.availability === "archived") return 3;
  return 2;
}

export function AgentsScreen() {
  const wsId = useWorkspaceId();
  const t = useT();
  const fmt = useFormatRelative();
  const { navigate } = useRouter();

  const {
    data: agents,
    isLoading: agentsLoading,
    isError: agentsError,
    refetch: refetchAgents,
  } = useQuery(agentListOptions(wsId));
  // Live signal for "who is running what" — the app has no WS, so poll.
  // Shares its cache key with the presence map's internal subscription, so
  // this interval also keeps workload badges fresh.
  const { data: snapshot } = useQuery({
    ...agentTaskSnapshotOptions(wsId),
    refetchInterval: 15_000,
  });
  const presence = useWorkspacePresenceMap(wsId);
  // Identifier lookup for "Working on MUL-123". Identifiers only change when
  // issues are created, so a slower poll is enough.
  const { data: issues } = useQuery({
    ...issueListOptions(wsId),
    refetchInterval: 30_000,
  });
  const { data: squads } = useQuery(squadListOptions(wsId));
  const { data: sessions } = useQuery(chatSessionsOptions(wsId));
  const createSession = useCreateChatSession();
  const [chatBusyId, setChatBusyId] = useState<string | null>(null);

  const activeAgents = useMemo(
    () => (agents ?? []).filter((a) => !a.archived_at),
    [agents],
  );

  // Newest running task per agent (join target for "working on").
  const runningByAgent = useMemo(() => {
    const m = new Map<string, AgentTask>();
    for (const task of snapshot ?? []) {
      if (task?.status !== "running" || !task.agent_id) continue;
      const prev = m.get(task.agent_id);
      if (!prev || (task.started_at ?? "") > (prev.started_at ?? "")) {
        m.set(task.agent_id, task);
      }
    }
    return m;
  }, [snapshot]);

  const identifierByIssue = useMemo(() => {
    const m = new Map<string, string>();
    for (const issue of issues ?? []) {
      if (issue?.id && issue.identifier) m.set(issue.id, issue.identifier);
    }
    return m;
  }, [issues]);

  // Squad leaders get the LEAD tag — one cheap workspace query.
  const leadIds = useMemo(() => {
    const s = new Set<string>();
    for (const squad of squads ?? []) {
      if (!squad?.archived_at && squad.leader_id) s.add(squad.leader_id);
    }
    return s;
  }, [squads]);

  const detailFor = (agentId: string): AgentPresenceDetail =>
    presence.byAgent.get(agentId) ?? (presence.loading ? LOADING_DETAIL : OFFLINE_DETAIL);

  const sortedAgents = useMemo(
    () =>
      [...activeAgents].sort((a, b) => {
        const ra = presenceRank(detailFor(a.id));
        const rb = presenceRank(detailFor(b.id));
        if (ra !== rb) return ra - rb;
        return a.name.localeCompare(b.name);
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [activeAgents, presence.byAgent, presence.loading],
  );

  const runningCount = activeAgents.reduce(
    (n, a) => (detailFor(a.id).workload === "working" ? n + 1 : n),
    0,
  );

  const openChat = async (agent: Agent) => {
    if (chatBusyId) return;
    haptic("light");
    const existing = (sessions ?? []).find(
      (s) => s?.agent_id === agent.id && s.status === "active",
    );
    if (existing) {
      navigate({ name: "chat-session", id: existing.id });
      return;
    }
    setChatBusyId(agent.id);
    try {
      const session = await createSession.mutateAsync({ agent_id: agent.id });
      navigate({ name: "chat-session", id: session.id });
    } catch {
      /* creation failed — stay on the tab, the button re-enables for retry */
    } finally {
      setChatBusyId(null);
    }
  };

  if (agentsLoading) return <TabSkeleton />;
  if (agentsError) return <QueryError onRetry={() => void refetchAgents()} />;

  if (activeAgents.length === 0) {
    return (
      <CenterMessage
        icon={<Bot className="size-7 text-muted-foreground" />}
        title={t("agents.none")}
        subtitle={t("agents.noneSub")}
      />
    );
  }

  return (
    <div className="flex min-h-0 flex-1 animate-ag-fade-in flex-col gap-2.5 overflow-y-auto px-4 py-2.5">
      {/* Title row + running pill */}
      <div className="flex items-center gap-2.5 px-1 pb-1">
        <h1 className="flex-1 text-[26px] font-bold tracking-[-0.4px] text-foreground">
          {t("agents.title")}
        </h1>
        <span
          className={cn(
            "flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full px-3 py-1.5 text-xs font-semibold",
            runningCount > 0 ? "bg-success/10 text-success" : "bg-muted text-muted-foreground",
          )}
        >
          <span
            className={cn(
              "size-[7px] rounded-full",
              runningCount > 0 ? "animate-ag-shimmer bg-success" : "bg-muted-foreground/40",
            )}
          />
          {t("agents.running", { n: runningCount })}
        </span>
      </div>

      {sortedAgents.map((agent) => {
        const detail = detailFor(agent.id);
        const offline =
          detail.availability === "offline" || detail.availability === "archived";
        const runningTask = runningByAgent.get(agent.id);
        const identifier = runningTask?.issue_id
          ? identifierByIssue.get(runningTask.issue_id)
          : undefined;
        const startedAt = runningTask?.started_at ?? runningTask?.created_at ?? null;

        return (
          <div
            key={agent.id}
            className="rounded-xl border border-border bg-card p-4 shadow-[0_1px_2px_rgba(9,9,11,0.04)] dark:shadow-none"
          >
            <div className="flex items-center gap-3">
              <AgentAvatar size={40} status={avatarStatus(detail)} />

              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-1.5">
                  <span
                    className={cn(
                      "truncate text-[15px] font-semibold",
                      offline ? "text-muted-foreground" : "text-foreground",
                    )}
                  >
                    {agent.name}
                  </span>
                  {leadIds.has(agent.id) && <AgentTag label="LEAD" />}
                </div>

                <div className="mt-0.5 truncate text-[12.5px] text-muted-foreground">
                  {detail.workload === "working" ? (
                    <>
                      {t("agents.workingOn")}
                      {identifier && (
                        <>
                          {" "}
                          <span className="font-mono text-[11.5px]">{identifier}</span>
                        </>
                      )}
                      {startedAt && <> · {fmt(startedAt)}</>}
                    </>
                  ) : detail.queuedCount > 0 ? (
                    t("agents.queued", { n: detail.queuedCount })
                  ) : offline ? (
                    t("agents.offline")
                  ) : (
                    t("agents.idle")
                  )}
                </div>

                {/* Subtle live-work signal — no per-case progress exists in the API */}
                {runningTask && (
                  <div className="mt-1.5 h-0.5 w-full animate-ag-shimmer rounded-full bg-info/60" />
                )}
              </div>

              {/* 36px visual disc inside a 44px tap target */}
              <button
                type="button"
                aria-label={t("agents.chatAria", { agent: agent.name })}
                onClick={() => void openChat(agent)}
                disabled={chatBusyId !== null}
                className="group -mr-1 flex size-11 shrink-0 items-center justify-center"
              >
                <span
                  className={cn(
                    "flex size-9 items-center justify-center rounded-full bg-muted text-foreground/70 transition-colors group-active:bg-muted/80",
                    chatBusyId === agent.id && "opacity-50",
                  )}
                >
                  <MessageCircle className="size-[19px]" />
                </span>
              </button>
            </div>
          </div>
        );
      })}
    </div>
  );
}
