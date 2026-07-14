import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, Send } from "lucide-react";
import {
  chatSessionOptions,
  chatMessagesOptions,
  pendingChatTaskOptions,
} from "@agora/core/chat/queries";
import { agentListOptions } from "@agora/core/workspace/queries";
import { agentTaskSnapshotOptions } from "@agora/core/agents";
import { issueDetailOptions } from "@agora/core/issues/queries";
import { getApi } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core/hooks";
import type { ChatMessage } from "@agora/core/types";
import { useRouter } from "../platform/navigation";
import { AgentAvatar } from "../components/agent-avatar";
import { TabSkeleton } from "../components/skeleton";
import { Markdown } from "../components/markdown";
import { ExpandableText } from "../components/expandable";
import { cn } from "../lib/cn";
import { haptic } from "../telegram/sdk";
import { useT } from "../i18n";

// 6b "agent workspace" — chat with an agent: identity header with live
// status, context card (what the agent is working on right now), message
// log, shimmer typing indicator, pill composer.

export function ChatSessionScreen({ sessionId }: { sessionId: string }) {
  const wsId = useWorkspaceId();
  const { back, navigate } = useRouter();
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

  // Context card: does this session's agent have a running task on an issue?
  // The task snapshot is the app-wide presence source (one workspace fetch);
  // 30s poll matches its staleTime since the Mini App has no WS.
  const { data: taskSnapshot = [] } = useQuery({
    ...agentTaskSnapshotOptions(wsId),
    refetchInterval: 30_000,
  });
  const runningIssueTask = session
    ? taskSnapshot.find(
        (tk) =>
          tk.agent_id === session.agent_id && tk.status === "running" && tk.issue_id !== "",
      )
    : undefined;
  const contextIssueId = runningIssueTask?.issue_id ?? "";
  const { data: contextIssue } = useQuery({
    ...issueDetailOptions(wsId, contextIssueId),
    enabled: contextIssueId !== "",
  });

  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  // Auto-grow the composer with the draft (capped by max-h-28) — a one-line
  // pill that hides its own overflow makes longer replies unreadable.
  useLayoutEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 112)}px`;
  }, [input]);

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
    <div className="flex min-h-0 flex-1 animate-ag-fade-in flex-col">
      {/* Agent header — identity + live status. Safe-area top padding stays
          here because the shell only pads root-tab screens. */}
      <header className="flex shrink-0 items-center gap-2.5 border-b border-border bg-card px-4 pb-2.5 pt-[max(env(safe-area-inset-top),0.625rem)]">
        <button
          type="button"
          onClick={back}
          aria-label={t("common.back")}
          className="-ml-2.5 flex size-11 shrink-0 items-center justify-center text-brand"
        >
          <ChevronLeft className="size-5" />
        </button>
        <AgentAvatar size={38} status={isWorking ? "running" : "idle"} />
        <div className="min-w-0 flex-1">
          <div className="truncate text-[15px] font-semibold text-foreground">{agentName}</div>
          <div
            className={cn(
              "truncate text-[11.5px] font-medium",
              isWorking ? "text-warning" : "text-muted-foreground",
            )}
          >
            {isWorking ? t("chat.isWorking", { agent: agentName }) : t("chat.aiAgent")}
          </div>
        </div>
      </header>

      {/* Context card — only when this agent is actively running a task on an
          issue. Pinned under the header so the "what is it doing" anchor stays
          visible while the log scrolls. */}
      {runningIssueTask && contextIssue && (
        <div className="shrink-0 px-4 pt-2.5">
          <button
            type="button"
            onClick={() => navigate({ name: "issue", id: contextIssue.id })}
            className="flex w-full flex-col gap-1.5 rounded-xl border border-border bg-card px-4 py-[13px] text-left shadow-[0_1px_2px_rgba(9,9,11,0.04)] active:border-brand dark:shadow-none"
          >
            <div className="text-[11px] font-semibold uppercase tracking-[0.07em] text-muted-foreground">
              {t("agents.workingOn")}
            </div>
            <div className="flex items-center gap-2">
              <span className="shrink-0 font-mono text-xs text-muted-foreground">
                {contextIssue.identifier}
              </span>
              <span className="min-w-0 flex-1 truncate text-[12.5px] font-medium text-foreground">
                {contextIssue.title}
              </span>
              <ChevronRight className="size-3.5 shrink-0 text-muted-foreground/70" />
            </div>
          </button>
        </div>
      )}

      <div ref={scrollRef} className="flex-1 space-y-3 overflow-y-auto px-4 py-3">
        {isLoading ? (
          <TabSkeleton />
        ) : messages.length === 0 ? (
          <div className="pt-8 text-center text-sm text-muted-foreground">
            {t("chat.startConvo")}
          </div>
        ) : (
          messages.map((m) => <MessageBubble key={m.id} message={m} />)
        )}
        {isWorking && <TypingBubble />}
      </div>

      {/* Composer — pill input + brand send disc. */}
      <div className="flex shrink-0 items-end gap-2.5 border-t border-border bg-card px-4 pb-[max(env(safe-area-inset-bottom),0.75rem)] pt-2.5">
        <textarea
          ref={inputRef}
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
          className="max-h-28 min-w-0 flex-1 resize-none rounded-2xl bg-muted px-4 py-[11px] text-sm leading-[1.4] text-foreground outline-none placeholder:text-muted-foreground"
        />
        {/* 44px effective tap target around the 38px visual disc. */}
        <button
          type="button"
          onClick={send}
          disabled={!input.trim() || sending}
          aria-label="Send"
          className="-m-[3px] flex size-11 shrink-0 items-center justify-center disabled:opacity-50"
        >
          <span className="flex size-[38px] items-center justify-center rounded-full bg-brand text-brand-foreground active:brightness-90">
            <Send className="size-[19px]" />
          </span>
        </button>
      </div>
    </div>
  );
}

function MessageBubble({ message }: { message: ChatMessage }) {
  const isUser = message.role === "user";
  if (isUser) {
    return (
      <div className="flex justify-end">
        <div className="max-w-[78%] whitespace-pre-wrap break-words rounded-[16px_16px_5px_16px] bg-brand px-3.5 py-[11px] text-[13.5px] leading-normal text-brand-foreground">
          {message.content}
        </div>
      </div>
    );
  }
  return (
    <div className="flex items-end gap-2.5">
      <AgentAvatar size={30} className="mb-0.5" />
      <div className="max-w-[78%] break-words rounded-[16px_16px_16px_5px] border border-border bg-card px-3.5 py-[11px] leading-normal text-foreground">
        {/* Agent replies get long — collapse behind "Show more". */}
        <ExpandableText>
          <Markdown content={message.content} className="text-[13.5px]" />
        </ExpandableText>
      </div>
    </div>
  );
}

// "Agent is typing" — an agent bubble with three shimmering dots, staggered
// so they read as a wave rather than a blink.
function TypingBubble() {
  return (
    <div className="flex items-end gap-2.5">
      <AgentAvatar size={30} className="mb-0.5" />
      <div className="flex items-center gap-1 rounded-[16px_16px_16px_5px] border border-border bg-card px-3.5 py-[15px]">
        {[0, 1, 2].map((i) => (
          <span
            key={i}
            className="size-1.5 animate-ag-shimmer rounded-full bg-muted-foreground/70"
            style={{ animationDelay: `${i * 0.18}s` }}
          />
        ))}
      </div>
    </div>
  );
}
