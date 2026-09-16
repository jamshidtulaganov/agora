"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Sparkles } from "lucide-react";
import {
  assistantAvailabilityOptions,
  assistantSessionListOptions,
  useAssistantStore,
  useCreateAssistantSession,
  useUpdateAssistantSession,
  useDeleteAssistantSession,
} from "@agora/core/assistant";
import { useCurrentWorkspace } from "@agora/core/paths";
import { PageHeader } from "../layout/page-header";
import { useT } from "../i18n";
import { SessionRail } from "./components/session-rail";
import { ActiveConversation } from "./components/active-conversation";
import { ArtifactPane } from "./components/artifact-pane";
import { AssistantLauncher } from "./components/launcher";
import { AssistantNotConfiguredState } from "./components/not-configured-state";
import { useHealActiveAssistantSession } from "./use-active-session";
import { messageContext } from "./lib/message-context";
import type { InitialAssistantMessage } from "./components/active-conversation";

export function AssistantPage() {
  const { t } = useT("assistant");
  const workspace = useCurrentWorkspace();

  const { data: availability, isLoading: isAvailabilityLoading } = useQuery(
    assistantAvailabilityOptions(),
  );
  const enabled = availability?.enabled === true;

  const { data: sessions = [] } = useQuery({
    ...assistantSessionListOptions(),
    enabled,
  });

  const activeSessionId = useAssistantStore((s) => s.activeSessionId);
  const setActiveSession = useAssistantStore((s) => s.setActiveSession);
  const runningSessionIds = new Set(sessions.filter((session) =>
    session.latest_run && (session.latest_run.status === "queued" || session.latest_run.status === "running"),
  ).map((session) => session.id));
  // Selects a primitive, not a derived object — a fresh object here would
  // re-render the page on every store write (see CLAUDE.md, Zustand footguns).
  const openArtifactId = useAssistantStore((s) =>
    activeSessionId ? (s.openArtifactId[activeSessionId] ?? null) : null,
  );
  const setOpenArtifact = useAssistantStore((s) => s.setOpenArtifact);

  const createSession = useCreateAssistantSession();
  const updateSession = useUpdateAssistantSession();
  const deleteSession = useDeleteAssistantSession();

  // A message sent before any session existed (empty-state / draft compose):
  // the session is created first, then this is delivered once
  // ActiveConversation mounts bound to the real session id.
  const [pendingInitialMessage, setPendingInitialMessage] = useState<InitialAssistantMessage | null>(null);

  useHealActiveAssistantSession(sessions, enabled);

  if (isAvailabilityLoading) {
    return (
      <div className="flex flex-1 min-h-0 flex-col">
        <PageHeader className="gap-2">
          <Sparkles className="h-4 w-4 text-muted-foreground" />
        </PageHeader>
      </div>
    );
  }

  if (!enabled) {
    return (
      <div className="flex flex-1 min-h-0 flex-col">
        <PageHeader className="gap-2">
          <Sparkles className="h-4 w-4 text-muted-foreground" />
        </PageHeader>
        <AssistantNotConfiguredState />
      </div>
    );
  }

  const handleCreateSession = () => {
    createSession.mutate(
      workspace ? { focus_workspace_id: workspace.id } : undefined,
      { onSuccess: (session) => setActiveSession(session.id) },
    );
  };

  const handleDeleteSession = (id: string) => {
    if (activeSessionId === id) setActiveSession(null);
    deleteSession.mutate(id);
  };

  const handleSendFromDraft = (content: string) => {
    const initialMessage = { content, request_id: crypto.randomUUID(), context: messageContext(workspace?.id ?? null) };
    createSession.mutate(
      workspace ? { focus_workspace_id: workspace.id } : undefined,
      {
        onSuccess: (session) => {
          setActiveSession(session.id);
          setPendingInitialMessage(initialMessage);
        },
        onError: () => toast.error(t(($) => $.toast.send_failed)),
      },
    );
  };

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeader className="gap-2">
        <Sparkles className="h-4 w-4 text-muted-foreground" />
        <h1 className="text-sm font-medium">{t(($) => $.session_rail.title)}</h1>
      </PageHeader>
      <div className="flex flex-1 min-h-0">
        <SessionRail
          sessions={sessions}
          activeSessionId={activeSessionId}
          runningSessionIds={runningSessionIds}
          onSelect={setActiveSession}
          onCreate={handleCreateSession}
          onRename={(id, title) => updateSession.mutate({ sessionId: id, title })}
          onDelete={handleDeleteSession}
        />
        <div className="flex min-w-0 flex-1 flex-col">
          {activeSessionId ? (
            <ActiveConversation
              key={activeSessionId}
              sessionId={activeSessionId}
              initialMessage={pendingInitialMessage}
              onInitialMessageConsumed={() => setPendingInitialMessage(null)}
              onOpenArtifact={(artifactId) => setOpenArtifact(activeSessionId, artifactId)}
            />
          ) : (
            <DraftConversation onSend={handleSendFromDraft} isCreating={createSession.isPending} scopeLabel={t(($) => $.composer.scope_workspace, { workspace: workspace?.name ?? t(($) => $.composer.scope_all) })} />
          )}
        </div>
        {activeSessionId && openArtifactId && (
          <ArtifactPane
            key={openArtifactId}
            artifactId={openArtifactId}
            onClose={() => setOpenArtifact(activeSessionId, null)}
          />
        )}
      </div>
    </div>
  );
}

/** Shown before any session exists — example prompts prefill the composer;
 *  sending creates the session and delivers the first message to it. */
function DraftConversation({
  onSend,
  isCreating,
  scopeLabel,
}: {
  onSend: (content: string) => void;
  isCreating: boolean;
  scopeLabel: string;
}) {
  const [value, setValue] = useState("");

  return (
    <AssistantLauncher
      value={value}
      onValueChange={setValue}
      onSend={onSend}
      isSending={isCreating}
      scopeLabel={scopeLabel}
    />
  );
}
