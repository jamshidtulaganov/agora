"use client";

import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { PanelLeft, Sparkles } from "lucide-react";
import { cn } from "@agora/ui/lib/utils";
import { Button } from "@agora/ui/components/ui/button";
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
import { useArtifactWorkbench } from "./use-artifact-workbench";
import { WorkbenchDivider } from "./components/workbench-divider";
import { useWorkbenchResize } from "./components/use-workbench-resize";
import { messageContext, targetWorkspaceId } from "./lib/message-context";
import type { InitialAssistantMessage } from "./components/active-conversation";
import { AssistantComposeResources } from "./components/compose-resources";

const NEW_SESSION_COMPOSER = "__new__";

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
  const setComposerContext = useAssistantStore((s) => s.setComposerContext);
  const runningSessionIds = new Set(sessions.filter((session) =>
    session.latest_run && (session.latest_run.status === "queued" || session.latest_run.status === "running"),
  ).map((session) => session.id));
  // The workbench: which artifact the pane shows, the pane opening itself when
  // the agent writes one, and the "updating" hint while it is still writing.
  const { openArtifactId, openArtifact, isUpdating } = useArtifactWorkbench(
    activeSessionId,
    enabled,
  );
  const workbenchRef = useRef<HTMLDivElement>(null);
  const resize = useWorkbenchResize(workbenchRef);

  const createSession = useCreateAssistantSession();
  const updateSession = useUpdateAssistantSession();
  const deleteSession = useDeleteAssistantSession();

  // A message sent before any session existed (empty-state / draft compose):
  // the session is created first, then this is delivered once
  // ActiveConversation mounts bound to the real session id.
  const [pendingInitialMessage, setPendingInitialMessage] = useState<InitialAssistantMessage | null>(null);
  // Narrow viewports fold the rail away; the header toggle brings it back as
  // an overlay. Ephemeral by design — a remembered panel state is one more
  // thing to be surprised by.
  const [isRailOpen, setRailOpen] = useState(false);

  useHealActiveAssistantSession(sessions, enabled);

  const handleCreateSession = () => {
    createSession.mutate(
      workspace ? { focus_workspace_id: workspace.id } : undefined,
      { onSuccess: (session) => setActiveSession(session.id) },
    );
  };

  // ⌘/Ctrl+K already belongs to global search (search/search-command.tsx), so
  // "new chat" takes ⌘/Ctrl+Shift+O — page-scoped, registered only while the
  // assistant page is mounted. The composer autofocuses on the session
  // switch, so the shortcut lands the caret in an empty chat.
  useEffect(() => {
    if (!enabled) return;
    const handleKeyDown = (event: KeyboardEvent) => {
      if (!event.shiftKey || !(event.metaKey || event.ctrlKey) || event.altKey) return;
      if (event.key.toLowerCase() !== "o") return;
      event.preventDefault();
      handleCreateSession();
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
    // handleCreateSession closes over stable mutation/store handles.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, workspace?.id]);

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

  const handleDeleteSession = (id: string) => {
    if (activeSessionId === id) setActiveSession(null);
    deleteSession.mutate(id);
  };

  const handleSelectSession = (id: string) => {
    setActiveSession(id);
    setRailOpen(false);
  };

  const handleSendFromDraft = (content: string) => {
    const selection = useAssistantStore.getState().composerContextBySession[NEW_SESSION_COMPOSER];
    const initialMessage = { content, request_id: crypto.randomUUID(), context: messageContext(workspace?.id ?? null, selection) };
    // A brand-new session has no focus to override, so it opens on the
    // workspace this first message is actually going to — a session whose
    // chip said one workspace while its composer said another would be two
    // truthful labels contradicting each other.
    const focusWorkspaceId = targetWorkspaceId(workspace?.id ?? null, selection);
    createSession.mutate(
      focusWorkspaceId ? { focus_workspace_id: focusWorkspaceId } : undefined,
      {
        onSuccess: (session) => {
          if (selection) setComposerContext(session.id, selection);
          setActiveSession(session.id);
          setPendingInitialMessage(initialMessage);
        },
        onError: () => toast.error(t(($) => $.toast.send_failed)),
      },
    );
  };

  const railToggleLabel = isRailOpen
    ? t(($) => $.session_rail.hide_chats)
    : t(($) => $.session_rail.show_chats);

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <PageHeader className="gap-2">
        <Button
          type="button"
          size="icon-sm"
          variant="ghost"
          className="sm:hidden"
          aria-expanded={isRailOpen}
          aria-label={railToggleLabel}
          title={railToggleLabel}
          onClick={() => setRailOpen((open) => !open)}
        >
          <PanelLeft />
        </Button>
        <Sparkles className="h-4 w-4 text-muted-foreground" />
        <h1 className="text-sm font-medium">{t(($) => $.session_rail.title)}</h1>
      </PageHeader>
      <div className="relative flex flex-1 min-h-0">
        <div
          className={cn(
            "shrink-0 bg-background max-sm:absolute max-sm:inset-y-0 max-sm:left-0 max-sm:z-20 max-sm:shadow-xl",
            isRailOpen ? "block" : "hidden sm:block",
          )}
        >
          <SessionRail
            sessions={sessions}
            activeSessionId={activeSessionId}
            runningSessionIds={runningSessionIds}
            onSelect={handleSelectSession}
            onCreate={handleCreateSession}
            onRename={(id, title) => updateSession.mutate({ sessionId: id, title })}
            onDelete={handleDeleteSession}
          />
        </div>
        <div ref={workbenchRef} className="flex min-w-0 flex-1">
          <div className="flex min-w-0 flex-1 flex-col">
            {activeSessionId ? (
              <ActiveConversation
                key={activeSessionId}
                sessionId={activeSessionId}
                initialMessage={pendingInitialMessage}
                onInitialMessageConsumed={() => setPendingInitialMessage(null)}
                onOpenArtifact={openArtifact}
              />
            ) : (
              <DraftConversation onSend={handleSendFromDraft} isCreating={createSession.isPending} workspaceId={workspace?.id ?? null} scopeLabel={t(($) => $.composer.scope_workspace, { workspace: workspace?.name ?? t(($) => $.composer.scope_all) })} />
            )}
          </div>
          {activeSessionId && openArtifactId && (
            <>
              <WorkbenchDivider resize={resize} />
              <ArtifactPane
                key={openArtifactId}
                artifactId={openArtifactId}
                sessionId={activeSessionId}
                width={resize.paneWidth}
                isUpdating={isUpdating}
                onSwitchArtifact={openArtifact}
                onClose={() => openArtifact(null)}
              />
            </>
          )}
        </div>
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
  workspaceId,
}: {
  onSend: (content: string) => void;
  isCreating: boolean;
  scopeLabel: string;
  workspaceId: string | null;
}) {
  const [value, setValue] = useState("");
  const [isUploading, setUploading] = useState(false);

  return (
    <AssistantLauncher
      value={value}
      onValueChange={setValue}
      onSend={onSend}
      isSending={isCreating}
      sendUnavailable={isUploading}
      scopeLabel={scopeLabel}
      resourceControls={<AssistantComposeResources sessionId={NEW_SESSION_COMPOSER} workspaceId={workspaceId} onUploadingChange={setUploading} />}
    />
  );
}
