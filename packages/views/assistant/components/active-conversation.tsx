"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { ArrowDown } from "lucide-react";
import {
  assistantMessagesOptions,
  assistantRunsOptions,
  useAssistantStore,
  useSendAssistantMessage,
  useCancelAssistantRun,
} from "@agora/core/assistant";
import { useCurrentWorkspace } from "@agora/core/paths";
import { workspaceListOptions } from "@agora/core/workspace";
import { ApiError } from "@agora/core/api";
import { useT } from "../../i18n";
import { messageContext, type MessageContext } from "../lib/message-context";
import { isNearBottom } from "../lib/scroll";
import { followUpsForTranscript } from "../lib/follow-ups";
import { MessageList } from "./message-list";
import { WorkingIndicator } from "./working-indicator";
import { Composer } from "./composer";
import { FollowUpChips } from "./follow-up-chips";
import { AssistantContextChip } from "./context-chip";
import { AssistantLauncher } from "./launcher";

export interface InitialAssistantMessage {
  content: string;
  request_id: string;
  context: MessageContext;
}

interface ActiveConversationProps {
  sessionId: string;
  initialMessage: InitialAssistantMessage | null;
  onInitialMessageConsumed: () => void;
  compact?: boolean;
  onOpenArtifact?: (artifactId: string) => void;
}

// The page and panel can both mount the same session. Claim an initial request
// before dispatching it so a surface switch cannot submit it twice.
const claimedInitialRequests = new Set<string>();
const activeStatuses = new Set(["queued", "running"]);
// A run that stopped short of an answer — the transcript offers to resend.
// "cancelled" is excluded: the user asked for the stop.
const recoverableStatuses = new Set(["failed", "interrupted"]);

export function ActiveConversation({
  sessionId,
  initialMessage,
  onInitialMessageConsumed,
  compact,
  onOpenArtifact,
}: ActiveConversationProps) {
  const { t } = useT("assistant");
  const workspace = useCurrentWorkspace();
  const { data: messages = [], isLoading } = useQuery(assistantMessagesOptions(sessionId));
  const runsQuery = useQuery(assistantRunsOptions(sessionId));
  const runs = runsQuery.data ?? [];
  const { data: workspaces = [] } = useQuery({ ...workspaceListOptions(), enabled: false });
  const latestRun = runs.at(-1);
  const isRunning = !!latestRun && activeStatuses.has(latestRun.status);
  const draft = useAssistantStore((s) => s.draftsBySession[sessionId]);
  const setDraft = useAssistantStore((s) => s.setDraft);
  const value = draft?.content ?? "";
  const sendMessage = useSendAssistantMessage(sessionId);
  const cancelRun = useCancelAssistantRun(sessionId);
  const scrollRef = useRef<HTMLDivElement>(null);
  const submittingRef = useRef(false);

  // --- stick-to-bottom ---------------------------------------------------
  // Follow new content only while the reader is ALREADY at the bottom.
  // Someone who scrolled up to re-read an earlier answer is never yanked
  // back down — they get the jump pill instead.
  const stickToBottomRef = useRef(true);
  const smoothNextRef = useRef(false);
  const [isDetached, setDetached] = useState(false);
  const [hasNewBelow, setHasNewBelow] = useState(false);

  const scrollToBottom = useCallback((behavior: "auto" | "smooth") => {
    const el = scrollRef.current;
    if (!el) return;
    // jsdom (and older webviews) have no Element.scrollTo — the assignment
    // below is the universally supported form.
    if (behavior === "smooth" && typeof el.scrollTo === "function") {
      el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
    } else {
      el.scrollTop = el.scrollHeight;
    }
    stickToBottomRef.current = true;
    setDetached(false);
    setHasNewBelow(false);
  }, []);

  const handleScroll = () => {
    const el = scrollRef.current;
    if (!el) return;
    const atBottom = isNearBottom(el);
    stickToBottomRef.current = atBottom;
    setDetached(!atBottom);
    if (atBottom) setHasNewBelow(false);
  };

  const handleSendError = (err: unknown) => {
    toast.error(
      err instanceof ApiError && err.status === 409
        ? t(($) => $.toast.still_working)
        : t(($) => $.toast.send_failed),
    );
  };

  const submit = async (entry: InitialAssistantMessage) => {
    if (submittingRef.current || isRunning || !runsQuery.isSuccess) return;
    submittingRef.current = true;
    // Sending is the user's own action: re-attach to the bottom and follow it
    // down gently, even if they were reading further up a moment ago.
    stickToBottomRef.current = true;
    smoothNextRef.current = true;
    setDraft(sessionId, entry);
    try {
      await sendMessage.mutateAsync(entry);
      if (useAssistantStore.getState().draftsBySession[sessionId]?.request_id === entry.request_id) {
        setDraft(sessionId, null);
      }
    } catch (err) {
      handleSendError(err);
    } finally {
      submittingRef.current = false;
    }
  };

  useEffect(() => {
    if (!initialMessage || !runsQuery.isSuccess) return;
    if (!claimedInitialRequests.has(initialMessage.request_id)) {
      claimedInitialRequests.add(initialMessage.request_id);
      void submit(initialMessage).finally(() => claimedInitialRequests.delete(initialMessage.request_id));
    }
    onInitialMessageConsumed();
    // The request is claimed once across mounts; changing callback identities
    // must not dispatch it again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, initialMessage?.request_id, runsQuery.isSuccess]);

  useEffect(() => {
    if (stickToBottomRef.current) {
      const behavior = smoothNextRef.current ? "smooth" : "auto";
      smoothNextRef.current = false;
      scrollToBottom(behavior);
      return;
    }
    smoothNextRef.current = false;
    setHasNewBelow(true);
  }, [messages.length, isRunning, latestRun?.status, scrollToBottom]);

  const handleValueChange = (content: string) => {
    setDraft(sessionId, content ? { content, request_id: crypto.randomUUID() } : null);
  };

  const handleSend = (content: string) => {
    if (!runsQuery.isSuccess) return;
    const entry = draft?.content === content
      ? { ...draft, context: draft.context ?? messageContext(workspace?.id ?? null) }
      : { content, request_id: crypto.randomUUID(), context: messageContext(workspace?.id ?? null) };
    void submit(entry);
  };

  const handleStop = () => {
    if (isRunning && latestRun) cancelRun.mutate(latestRun.id);
  };

  // --- resend (retry a failed run / regenerate the last answer) -----------
  // One request id per source message, kept for the life of the mount: a
  // second click re-sends the SAME identity, so the server can dedupe it
  // instead of queueing a duplicate turn.
  const resendIdsRef = useRef(new Map<string, string>());
  const lastUserMessage = useMemo(() => {
    for (let index = messages.length - 1; index >= 0; index -= 1) {
      const message = messages[index]!;
      if (message.role === "user") return message;
    }
    return null;
  }, [messages]);

  const resendLastUserMessage = () => {
    if (!lastUserMessage || !runsQuery.isSuccess || isRunning) return;
    const content = lastUserMessage.content.trim();
    if (!content) return;
    // A draft holding the same words already carries an identity — reuse it
    // rather than minting a second one for the same intent.
    const reusableDraft = draft?.content === content ? draft : null;
    let requestId = reusableDraft?.request_id ?? resendIdsRef.current.get(lastUserMessage.id);
    if (!requestId) {
      requestId = crypto.randomUUID();
      resendIdsRef.current.set(lastUserMessage.id, requestId);
    }
    void submit({
      content,
      request_id: requestId,
      context: reusableDraft?.context ?? messageContext(workspace?.id ?? null),
    });
  };

  const targetWorkspaceId = draft?.context ? draft.context.workspace_id : workspace?.id ?? null;
  const targetWorkspace = targetWorkspaceId
    ? workspaces.find((item) => item.id === targetWorkspaceId)?.name ??
      (workspace?.id === targetWorkspaceId ? workspace.name : t(($) => $.composer.scope_previous))
    : t(($) => $.composer.scope_all);
  const scopeLabel = t(($) => $.composer.scope_workspace, { workspace: targetWorkspace }) +
    (runsQuery.isPending ? ` · ${t(($) => $.run.checking_status)}` : "");
  const notice = latestRun?.status === "failed"
    ? t(($) => $.run.failed)
    : latestRun?.status === "cancelled"
      ? t(($) => $.run.cancelled)
      : latestRun?.status === "interrupted"
        ? t(($) => $.run.interrupted)
        : null;
  const canResend = !!lastUserMessage && !isRunning && runsQuery.isSuccess;
  const showRetry = !!latestRun && recoverableStatuses.has(latestRun.status) && canResend;
  // Only ever the server's own message. Empty on every other status.
  const runError = latestRun?.status === "failed" ? latestRun.error?.trim() : null;
  const showEmptyState = !isLoading && messages.length === 0 && !isRunning && !notice && !runsQuery.isError;

  // Deterministic, no extra model call — see lib/follow-ups.ts. Hidden the
  // moment the user starts typing: they already know what they want next.
  const followUps = useMemo(
    () =>
      latestRun?.status === "completed" && !isRunning && value === ""
        ? followUpsForTranscript(messages)
        : [],
    [latestRun?.status, isRunning, value, messages],
  );

  // Rendered in both surfaces so the session's scope is visible before the
  // first message as well as after it — that scope is what tools default to.
  const contextChip = <AssistantContextChip sessionId={sessionId} />;

  if (showEmptyState) {
    return (
      <AssistantLauncher
        value={value}
        onValueChange={handleValueChange}
        onSend={handleSend}
        isSending={sendMessage.isPending}
        sendUnavailable={!runsQuery.isSuccess}
        scopeLabel={scopeLabel}
        contextChip={contextChip}
        compact={compact}
      />
    );
  }

  return (
    <>
      <div className="relative flex min-h-0 flex-1 flex-col">
        <div
          ref={scrollRef}
          onScroll={handleScroll}
          className="flex min-h-0 flex-1 flex-col overflow-y-auto overflow-x-hidden"
        >
          <MessageList
            messages={messages}
            onOpenArtifact={onOpenArtifact}
            onRegenerate={canResend ? resendLastUserMessage : undefined}
          />
          {followUps.length > 0 && (
            <FollowUpChips ids={followUps} onPick={handleValueChange} />
          )}
          {isRunning && <WorkingIndicator activeTool={latestRun?.active_tool ?? null} />}
          {notice && (
            <div role="status" className="mx-auto mb-3 w-full max-w-2xl rounded-md border border-destructive/20 bg-destructive/5 px-4 py-3 text-sm text-foreground">
              <p>{notice}</p>
              {runError && (
                <p className="mt-1 line-clamp-2 break-words text-xs text-muted-foreground">
                  {t(($) => $.run.error_detail_label)}: {runError}
                </p>
              )}
              <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.run.effects_notice)}</p>
              {showRetry && (
                <button
                  type="button"
                  onClick={resendLastUserMessage}
                  className="mt-2 inline-flex h-7 cursor-pointer items-center rounded-md border border-input bg-background px-2.5 text-xs font-medium transition-colors hover:bg-accent"
                >
                  {t(($) => $.run.retry)}
                </button>
              )}
            </div>
          )}
          {runsQuery.isError && (
            <div role="status" className="mx-auto mb-3 w-full max-w-2xl px-4 text-sm text-muted-foreground">
              {t(($) => $.run.status_unavailable)}{" "}
              <button type="button" className="underline" onClick={() => void runsQuery.refetch()}>{t(($) => $.run.retry_status)}</button>
            </div>
          )}
        </div>
        {isDetached && hasNewBelow && (
          <button
            type="button"
            onClick={() => scrollToBottom("smooth")}
            className="absolute inset-x-0 bottom-3 mx-auto flex w-fit animate-in fade-in cursor-pointer items-center gap-1.5 rounded-full border bg-card px-3 py-1.5 text-xs text-muted-foreground shadow-sm transition-colors hover:text-foreground"
          >
            <ArrowDown className="size-3" />
            {t(($) => $.transcript.jump_to_latest)}
          </button>
        )}
      </div>
      <Composer
        value={value}
        onValueChange={handleValueChange}
        onSend={handleSend}
        onStop={handleStop}
        isRunning={isRunning}
        isSending={sendMessage.isPending}
        sendUnavailable={!runsQuery.isSuccess}
        scopeLabel={scopeLabel}
        contextChip={contextChip}
        // ActiveConversation is keyed by session id, so this remounts — and
        // lands the caret in the composer — on every session switch.
        autoFocus
      />
    </>
  );
}
