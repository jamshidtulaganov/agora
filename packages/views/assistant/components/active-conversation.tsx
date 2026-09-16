"use client";

import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
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
import { MessageList } from "./message-list";
import { WorkingIndicator } from "./working-indicator";
import { Composer } from "./composer";
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
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [messages.length, isRunning, latestRun?.status]);

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
  const showEmptyState = !isLoading && messages.length === 0 && !isRunning && !notice && !runsQuery.isError;

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
      <div ref={scrollRef} className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        <MessageList messages={messages} onOpenArtifact={onOpenArtifact} />
        {isRunning && <WorkingIndicator activeTool={latestRun?.active_tool ?? null} />}
        {notice && (
          <div role="status" className="mx-auto mb-3 w-full max-w-2xl rounded-md border border-destructive/20 bg-destructive/5 px-4 py-3 text-sm text-foreground">
            <p>{notice}</p>
            <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.run.effects_notice)}</p>
          </div>
        )}
        {runsQuery.isError && (
          <div role="status" className="mx-auto mb-3 w-full max-w-2xl px-4 text-sm text-muted-foreground">
            {t(($) => $.run.status_unavailable)}{" "}
            <button type="button" className="underline" onClick={() => void runsQuery.refetch()}>{t(($) => $.run.retry_status)}</button>
          </div>
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
      />
    </>
  );
}
