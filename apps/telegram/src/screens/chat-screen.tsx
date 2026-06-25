import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus, Bot, ChevronRight } from "lucide-react";
import { chatSessionsOptions } from "@agora/core/chat/queries";
import { useCreateChatSession } from "@agora/core/chat/mutations";
import { agentListOptions } from "@agora/core/workspace/queries";
import { useWorkspaceId } from "@agora/core/hooks";
import { useRouter } from "../platform/navigation";
import { CenterMessage } from "../components/center-message";
import { BottomSheet } from "../components/bottom-sheet";
import { formatRelative } from "../lib/time";
import { haptic } from "../telegram/sdk";
import { cn } from "../lib/cn";

export function ChatScreen() {
  const wsId = useWorkspaceId();
  const { data: sessions = [], isLoading } = useQuery(chatSessionsOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const createSession = useCreateChatSession();
  const { navigate } = useRouter();
  const [picking, setPicking] = useState(false);

  const activeSessions = sessions.filter((s) => s.status === "active");
  const activeAgents = agents.filter((a) => !a.archived_at);
  const agentName = (id: string) => agents.find((a) => a.id === id)?.name ?? "Agent";

  const startChat = async (agentId: string) => {
    haptic("light");
    try {
      const session = await createSession.mutateAsync({ agent_id: agentId });
      setPicking(false);
      navigate({ name: "chat-session", id: session.id });
    } catch {
      /* keep the picker open so the user can retry */
    }
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 items-center justify-between border-b border-border px-4 py-2">
        <span className="text-sm font-semibold text-foreground">Chats</span>
        <button
          type="button"
          onClick={() => setPicking(true)}
          className="flex items-center gap-1 rounded-full bg-[var(--brand,theme(colors.blue.600))] px-3 py-1.5 text-xs font-semibold text-white"
        >
          <Plus className="size-3.5" />
          New chat
        </button>
      </div>

      {isLoading ? (
        <CenterMessage spinner title="Loading chats…" />
      ) : activeSessions.length === 0 ? (
        <CenterMessage
          title="No chats yet"
          subtitle="Start a conversation with one of your workspace agents."
        />
      ) : (
        <ul className="flex-1 divide-y divide-border overflow-y-auto">
          {activeSessions.map((s) => (
            <li key={s.id}>
              <button
                type="button"
                onClick={() => {
                  haptic("light");
                  navigate({ name: "chat-session", id: s.id });
                }}
                className="flex w-full items-center gap-3 px-4 py-3 text-left transition-colors active:bg-accent"
              >
                <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-[var(--brand,theme(colors.blue.600))]/10">
                  <Bot className="size-5 text-[var(--brand,theme(colors.blue.600))]" />
                </span>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate text-sm font-medium text-foreground">
                      {s.title || agentName(s.agent_id)}
                    </span>
                    <span className="shrink-0 text-[11px] text-muted-foreground">
                      {formatRelative(s.updated_at)}
                    </span>
                  </div>
                  <div className="truncate text-xs text-muted-foreground">
                    {agentName(s.agent_id)}
                  </div>
                </div>
                {s.has_unread && (
                  <span className="size-2 shrink-0 rounded-full bg-[var(--brand,theme(colors.blue.600))]" />
                )}
                <ChevronRight className="size-4 shrink-0 text-muted-foreground/60" />
              </button>
            </li>
          ))}
        </ul>
      )}

      <BottomSheet open={picking} onClose={() => setPicking(false)} title="Start a chat">
        {activeAgents.length === 0 ? (
          <div className="px-4 py-6 text-center text-sm text-muted-foreground">
            No agents available in this workspace.
          </div>
        ) : (
          <ul className="pb-2">
            {activeAgents.map((a) => (
              <li key={a.id}>
                <button
                  type="button"
                  onClick={() => startChat(a.id)}
                  disabled={createSession.isPending}
                  className={cn(
                    "flex w-full items-center gap-2.5 px-4 py-3 text-left text-sm active:bg-accent",
                    createSession.isPending && "opacity-50",
                  )}
                >
                  <Bot className="size-4 text-[var(--brand,theme(colors.blue.600))]" />
                  <span className="flex-1 truncate">{a.name}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </BottomSheet>
    </div>
  );
}
