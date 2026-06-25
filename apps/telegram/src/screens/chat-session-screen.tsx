import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, Send, Bot, Loader2 } from "lucide-react";
import {
  chatSessionOptions,
  chatMessagesOptions,
  pendingChatTaskOptions,
} from "@agora/core/chat/queries";
import { agentListOptions } from "@agora/core/workspace/queries";
import { getApi } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core/hooks";
import type { ChatMessage } from "@agora/core/types";
import { useRouter } from "../platform/navigation";
import { CenterMessage } from "../components/center-message";
import { haptic } from "../telegram/sdk";
import { cn } from "../lib/cn";

export function ChatSessionScreen({ sessionId }: { sessionId: string }) {
  const wsId = useWorkspaceId();
  const { back } = useRouter();
  const qc = useQueryClient();

  const { data: session } = useQuery(chatSessionOptions(wsId, sessionId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const pending = useQuery({ ...pendingChatTaskOptions(sessionId), refetchInterval: 2500 });
  const isWorking = !!pending.data?.task_id;
  const { data: messages = [], isLoading } = useQuery({
    ...chatMessagesOptions(sessionId),
    // WS invalidation already refreshes this; poll as a Telegram-webview
    // fallback only while the agent is working.
    refetchInterval: isWorking ? 2000 : false,
  });

  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);

  const agentName = session
    ? agents.find((a) => a.id === session.agent_id)?.name ?? "Agent"
    : "Agent";

  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [messages.length, isWorking]);

  const send = async () => {
    const content = input.trim();
    if (!content || sending) return;
    haptic("light");
    setSending(true);
    setInput("");
    try {
      await getApi().sendChatMessage(sessionId, content, []);
      qc.invalidateQueries({ queryKey: chatMessagesOptions(sessionId).queryKey });
      qc.invalidateQueries({ queryKey: pendingChatTaskOptions(sessionId).queryKey });
    } catch {
      setInput(content); // restore the draft so the user can retry
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex shrink-0 items-center gap-2 border-b border-border bg-card px-2 py-2 pt-[max(env(safe-area-inset-top),0.5rem)]">
        <button type="button" onClick={back} className="px-1 py-1 text-muted-foreground">
          <ChevronLeft className="size-5" />
        </button>
        <span className="flex size-7 items-center justify-center rounded-full bg-[var(--brand,theme(colors.blue.600))]/10">
          <Bot className="size-4 text-[var(--brand,theme(colors.blue.600))]" />
        </span>
        <span className="truncate text-sm font-semibold text-foreground">{agentName}</span>
      </header>

      <div ref={scrollRef} className="flex-1 space-y-2 overflow-y-auto px-3 py-3">
        {isLoading ? (
          <CenterMessage spinner title="Loading…" />
        ) : messages.length === 0 ? (
          <div className="pt-8 text-center text-sm text-muted-foreground">
            Send a message to start the conversation.
          </div>
        ) : (
          messages.map((m) => <MessageBubble key={m.id} message={m} />)
        )}
        {isWorking && (
          <div className="flex items-center gap-2 px-1 text-xs text-muted-foreground">
            <Loader2 className="size-3.5 animate-spin" />
            {agentName} is working…
          </div>
        )}
      </div>

      <div className="flex shrink-0 items-end gap-2 border-t border-border bg-card px-3 py-2 pb-[max(env(safe-area-inset-bottom),0.5rem)]">
        <textarea
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              void send();
            }
          }}
          rows={1}
          placeholder="Message…"
          className="max-h-32 flex-1 resize-none rounded-2xl bg-muted px-3 py-2 text-sm text-foreground outline-none placeholder:text-muted-foreground"
        />
        <button
          type="button"
          onClick={send}
          disabled={!input.trim() || sending}
          aria-label="Send"
          className="flex size-9 shrink-0 items-center justify-center rounded-full bg-[var(--brand,theme(colors.blue.600))] text-white disabled:opacity-40"
        >
          <Send className="size-4" />
        </button>
      </div>
    </div>
  );
}

function MessageBubble({ message }: { message: ChatMessage }) {
  const isUser = message.role === "user";
  return (
    <div className={cn("flex", isUser ? "justify-end" : "justify-start")}>
      <div
        className={cn(
          "max-w-[82%] whitespace-pre-wrap break-words rounded-2xl px-3 py-2 text-sm",
          isUser
            ? "bg-[var(--brand,theme(colors.blue.600))] text-white"
            : "bg-muted text-foreground",
        )}
      >
        {message.content}
      </div>
    </div>
  );
}
