"use client";

import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { motion } from "motion/react";
import { toast } from "sonner";
import { Check, ExternalLink, History, Maximize2, Minimize2, Plus, X } from "lucide-react";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { Button } from "@agora/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@agora/ui/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@agora/ui/components/ui/tooltip";
import {
  assistantAvailabilityOptions,
  assistantSessionListOptions,
  useAssistantPanelStore,
  useAssistantStore,
  useCreateAssistantSession,
} from "@agora/core/assistant";
import type { AssistantSession } from "@agora/core/types";
import { paths, useCurrentWorkspace, useWorkspaceSlug } from "@agora/core/paths";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";
import { useHealActiveAssistantSession } from "../use-active-session";
import { ActiveConversation } from "./active-conversation";
import { AssistantLauncher } from "./launcher";
import { PanelResizeHandles } from "./panel-resize-handles";
import { usePanelResize } from "./use-panel-resize";
import { messageContext, targetWorkspaceId } from "../lib/message-context";
import type { InitialAssistantMessage } from "./active-conversation";
import { AssistantComposeResources } from "./compose-resources";

const NEW_SESSION_COMPOSER = "__new__";

/** How many recent sessions the header picker lists before it stops. */
const RECENT_SESSION_LIMIT = 10;

/**
 * Floating bottom-right Assistant panel — the quick surface for the same
 * conversation the full `/:slug/assistant` page shows. It owns no realtime
 * logic of its own: the assistant WS updaters are registered globally in
 * `useRealtimeSync`, so this only ever reads the Query cache.
 *
 * Availability-gated: when the instance reports the assistant off, neither
 * this nor the FAB renders at all.
 */
export function AssistantPanel() {
  const isOpen = useAssistantPanelStore((s) => s.isOpen);
  const { data: availability } = useQuery(assistantAvailabilityOptions());

  if (availability?.enabled !== true || !isOpen) return null;

  return <AssistantPanelWindow />;
}

function AssistantPanelWindow() {
  const { t } = useT("assistant");
  const navigation = useNavigation();
  const workspace = useCurrentWorkspace();
  const slug = useWorkspaceSlug();

  const setOpen = useAssistantPanelStore((s) => s.setOpen);
  const isExpanded = useAssistantPanelStore((s) => s.isExpanded);

  const { data: sessions = [] } = useQuery(assistantSessionListOptions());
  const activeSessionId = useAssistantStore((s) => s.activeSessionId);
  const setActiveSession = useAssistantStore((s) => s.setActiveSession);
  const setOpenArtifact = useAssistantStore((s) => s.setOpenArtifact);
  const setComposerContext = useAssistantStore((s) => s.setComposerContext);
  useHealActiveAssistantSession(sessions, true);

  const createSession = useCreateAssistantSession();

  // A message sent before any session existed: the session is created first,
  // then this is delivered once ActiveConversation mounts bound to the real id.
  const [pendingInitialMessage, setPendingInitialMessage] = useState<InitialAssistantMessage | null>(null);
  const [draftValue, setDraftValue] = useState("");
  const [isUploading, setUploading] = useState(false);

  // Escape closes the panel — the same gesture every other dismissible
  // surface in the app answers to. Two deliberate exemptions: a keystroke
  // something else already consumed (the composer's slash menu calls
  // preventDefault), and any open dialog/menu/listbox, which owns Escape
  // until it is dismissed. Composer content is irrelevant: Escape never
  // discards a draft, it is persisted and waiting on reopen.
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      if (document.querySelector('[role="dialog"],[role="alertdialog"],[role="menu"],[role="listbox"]')) return;
      setOpen(false);
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [setOpen]);

  const windowRef = useRef<HTMLDivElement>(null);
  const { renderWidth, renderHeight, isAtMax, boundsReady, isDragging, toggleExpand, startDrag } =
    usePanelResize(windowRef);

  const handleNewChat = () => {
    createSession.mutate(workspace ? { focus_workspace_id: workspace.id } : undefined, {
      onSuccess: (session) => setActiveSession(session.id),
      onError: () => toast.error(t(($) => $.toast.send_failed)),
    });
  };

  const handleSendFromDraft = (content: string) => {
    const selection = useAssistantStore.getState().composerContextBySession[NEW_SESSION_COMPOSER];
    const initialMessage = { content, request_id: crypto.randomUUID(), context: messageContext(workspace?.id ?? null, selection) };
    // A brand-new session has no focus to override, so it opens on the
    // workspace this first message is actually going to — a session whose
    // chip said one workspace while its composer said another would be two
    // truthful labels contradicting each other.
    const focusWorkspaceId = targetWorkspaceId(workspace?.id ?? null, selection);
    createSession.mutate(focusWorkspaceId ? { focus_workspace_id: focusWorkspaceId } : undefined, {
      onSuccess: (session) => {
        if (selection) setComposerContext(session.id, selection);
        setActiveSession(session.id);
        setPendingInitialMessage(initialMessage);
      },
      onError: () => toast.error(t(($) => $.toast.send_failed)),
    });
  };

  // The full page lives under the workspace prefix; without a slug (shell
  // mounted before the workspace resolves) there is nowhere to send them.
  const handleOpenFullPage = () => {
    if (!slug) return;
    setOpen(false);
    navigation.push(paths.workspace(slug).assistant());
  };

  const expandLabel =
    isExpanded || isAtMax ? t(($) => $.panel.restore) : t(($) => $.panel.expand);

  // Wait for the first bounds measurement before fading in, otherwise the
  // panel flashes at its unclamped size for one frame.
  const isVisible = isExpanded || boundsReady;

  return (
    <motion.div
      ref={windowRef}
      className="absolute bottom-2 right-2 z-50 flex flex-col overflow-hidden rounded-xl bg-sidebar shadow-2xl ring-1 ring-foreground/10"
      style={{ transformOrigin: "bottom right" }}
      initial={{ opacity: 0, scale: 0.95, width: renderWidth, height: renderHeight }}
      animate={{
        opacity: isVisible ? 1 : 0,
        scale: isVisible ? 1 : 0.95,
        width: renderWidth,
        height: renderHeight,
      }}
      transition={{
        width: isDragging ? { duration: 0 } : { type: "spring", duration: 0.3, bounce: 0 },
        height: isDragging ? { duration: 0 } : { type: "spring", duration: 0.3, bounce: 0 },
        opacity: { duration: 0.15 },
        scale: { type: "spring", duration: 0.2, bounce: 0 },
      }}
    >
      <PanelResizeHandles onDragStart={startDrag} />

      <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
        <div className="flex min-w-0 items-center gap-1.5">
          <AgoraIcon noSpin className="size-4 shrink-0 text-foreground/70" />
          <span className="truncate text-sm font-medium">{t(($) => $.panel.title)}</span>
          <SessionPicker
            sessions={sessions}
            activeSessionId={activeSessionId}
            onSelect={setActiveSession}
          />
        </div>
        <div className="flex shrink-0 items-center gap-0.5">
          <PanelAction
            label={t(($) => $.panel.new_chat)}
            onClick={handleNewChat}
            disabled={createSession.isPending}
          >
            <Plus />
          </PanelAction>
          <PanelAction label={expandLabel} onClick={toggleExpand} pressed={isExpanded || isAtMax}>
            {isExpanded || isAtMax ? <Minimize2 /> : <Maximize2 />}
          </PanelAction>
          {slug && (
            <PanelAction label={t(($) => $.panel.open_full_page)} onClick={handleOpenFullPage}>
              <ExternalLink />
            </PanelAction>
          )}
          <PanelAction label={t(($) => $.panel.close)} onClick={() => setOpen(false)}>
            <X />
          </PanelAction>
        </div>
      </div>

      {activeSessionId ? (
        <ActiveConversation
          key={activeSessionId}
          sessionId={activeSessionId}
          initialMessage={pendingInitialMessage}
          onInitialMessageConsumed={() => setPendingInitialMessage(null)}
          compact
          // The artifact pane does not fit this popup: mark the artifact as
          // open on the session and hand off to the full page, reusing the
          // same close-and-navigate the "Open full page" action does.
          onOpenArtifact={(artifactId) => {
            setOpenArtifact(activeSessionId, artifactId);
            handleOpenFullPage();
          }}
        />
      ) : (
        <AssistantLauncher
          value={draftValue}
          onValueChange={setDraftValue}
          onSend={handleSendFromDraft}
          isSending={createSession.isPending}
          sendUnavailable={isUploading}
          scopeLabel={t(($) => $.composer.scope_workspace, { workspace: workspace?.name ?? t(($) => $.composer.scope_all) })}
          resourceControls={<AssistantComposeResources sessionId={NEW_SESSION_COMPOSER} workspaceId={workspace?.id ?? null} onUploadingChange={setUploading} />}
          compact
        />
      )}
    </motion.div>
  );
}

function PanelAction({
  label,
  onClick,
  disabled,
  pressed,
  children,
}: {
  label: string;
  onClick: () => void;
  disabled?: boolean;
  pressed?: boolean;
  children: React.ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={label}
            aria-pressed={pressed}
            disabled={disabled}
            className="text-muted-foreground"
            onClick={onClick}
          />
        }
      >
        {children}
      </TooltipTrigger>
      <TooltipContent side="top">{label}</TooltipContent>
    </Tooltip>
  );
}

/**
 * Recent-chats dropdown. The panel deliberately has no session rail — rename
 * and delete stay on the full page, where there is room for them.
 */
function SessionPicker({
  sessions,
  activeSessionId,
  onSelect,
}: {
  sessions: AssistantSession[];
  activeSessionId: string | null;
  onSelect: (id: string) => void;
}) {
  const { t } = useT("assistant");
  const recent = sessions.slice(0, RECENT_SESSION_LIMIT);

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label={t(($) => $.panel.recent_chats)}
        className="inline-flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground data-popup-open:bg-accent data-popup-open:text-foreground"
      >
        <History className="size-3.5" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" side="bottom" sideOffset={6} className="min-w-56 max-w-72">
        {recent.length === 0 ? (
          <div className="px-2 py-3 text-center text-xs text-muted-foreground">
            {t(($) => $.session_rail.empty)}
          </div>
        ) : (
          recent.map((session) => (
            <DropdownMenuItem key={session.id} onClick={() => onSelect(session.id)}>
              <Check
                className={
                  session.id === activeSessionId
                    ? "size-3.5 shrink-0"
                    : "size-3.5 shrink-0 opacity-0"
                }
              />
              <span className="truncate">
                {session.title.trim() || t(($) => $.session_rail.untitled)}
              </span>
            </DropdownMenuItem>
          ))
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
