import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, Send, Loader2 } from "lucide-react";
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
import { Avatar } from "../components/avatar";
import { haptic } from "../telegram/sdk";
import { useT } from "../i18n";

export function ChatSessionScreen({ sessionId }: { sessionId: string }) {
  const wsId = useWorkspaceId();
  const { back } = useRouter();
  const qc = useQueryClient();

  const t = useT();
  const { data: session } = useQuery(chatSessionOptions(wsId, sessionId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const pending = useQuery({ ...pendingChatTaskOptions(sessionId), refetchInterval: 2500 });
  const isWorking = !!pending.data?.task_id;
  const { data: messages = [], isLoading } = useQuery({
    ...chatMessagesOptions(sessionId),
    // WS invalidation refreshes this live; we also poll as a Telegram-webview
    // fallback (the in-app browser can drop the socket) — fast while the agent
    // is working, slow-but-steady otherwise so replies never look stuck.
    refetchInterval: isWorking ? 2000 : 5000,
  });

  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);

  const agentName = session
    ? agents.find((a) => a.id === session.agent_id)?.name ?? t("common.agent")
    : t("common.agent");

  // Clear the session's unread badge on open.
  useEffect(() => {
    getApi()
      .markChatSessionRead(sessionId)
      .catch(() => {});
  }, [sessionId]);

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

    // Optimistically show the user's message so it never appears to vanish on
    // send. The await below persists it server-side before we invalidate, so the
    // refetch replaces this temp row with the authoritative one (no duplicate).
    const tempId = `temp-${Date.now()}`;
    const optimistic = {
      id: tempId,
      chat_session_id: sessionId,
      role: "user",
      content,
      task_id: null,
      created_at: new Date().toISOString(),
    } as ChatMessage;
    qc.setQueryData<ChatMessage[]>(chatMessagesOptions(sessionId).queryKey, (old) => [
      ...(old ?? []),
      optimistic,
    ]);

    try {
      await getApi().sendChatMessage(sessionId, content, []);
      qc.invalidateQueries({ queryKey: chatMessagesOptions(sessionId).queryKey });
      qc.invalidateQueries({ queryKey: pendingChatTaskOptions(sessionId).queryKey });
    } catch {
      // Roll the optimistic row back and restore the draft so the user can retry.
      qc.setQueryData<ChatMessage[]>(chatMessagesOptions(sessionId).queryKey, (old) =>
        (old ?? []).filter((m) => m.id !== tempId),
      );
      setInput(content);
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="flex shrink-0 items-center gap-2.5 border-b border-border bg-card px-2 py-2 pt-[max(env(safe-area-inset-top),0.5rem)]">
        <button type="button" onClick={back} className="px-1 py-1 text-muted-foreground">
          <ChevronLeft className="size-5" />
        </button>
        <Avatar name={agentName} isAgent size={30} />
        <div className="min-w-0">
          <div className="truncate text-sm font-semibold text-foreground">{agentName}</div>
          <div className="text-[11px] text-muted-foreground">
            {isWorking ? t("chat.working") : t("chat.aiAgent")}
          </div>
        </div>
      </header>

      <div ref={scrollRef} className="flex-1 space-y-3 overflow-y-auto px-3 py-3">
        {isLoading ? (
          <CenterMessage spinner title={t("common.loading")} />
        ) : messages.length === 0 ? (
          <div className="pt-8 text-center text-sm text-muted-foreground">
            {t("chat.startConvo")}
          </div>
        ) : (
          messages.map((m) => (
            <MessageBubble key={m.id} message={m} agentName={agentName} />
          ))
        )}
        {isWorking && (
          <div className="flex items-center gap-2 pl-1 text-xs text-muted-foreground">
            <Loader2 className="size-3.5 animate-spin" />
            {t("chat.isWorking", { agent: agentName })}
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
          placeholder={t("chat.messagePlaceholder", { agent: agentName })}
          className="max-h-32 flex-1 resize-none rounded-2xl border border-border bg-background px-3 py-2 text-sm text-foreground outline-none placeholder:text-muted-foreground focus:border-ring"
        />
        <button
          type="button"
          onClick={send}
          disabled={!input.trim() || sending}
          aria-label="Send"
          className="flex size-9 shrink-0 items-center justify-center rounded-full bg-brand text-brand-foreground disabled:opacity-40"
        >
          <Send className="size-4" />
        </button>
      </div>
    </div>
  );
}

function MessageBubble({ message, agentName }: { message: ChatMessage; agentName: string }) {
  const isUser = message.role === "user";
  if (isUser) {
    return (
      <div className="flex justify-end">
        <div className="max-w-[82%] whitespace-pre-wrap break-words rounded-2xl rounded-br-md bg-brand px-3 py-2 text-sm text-brand-foreground">
          {message.content}
        </div>
      </div>
    );
  }
  return (
    <div className="flex items-end gap-2">
      <Avatar name={agentName} isAgent size={24} className="mb-0.5" />
      <div className="max-w-[82%] whitespace-pre-wrap break-words rounded-2xl rounded-bl-md bg-muted px-3 py-2 text-sm text-foreground ring-1 ring-foreground/5">
        {message.content}
      </div>
    </div>
  );
}
