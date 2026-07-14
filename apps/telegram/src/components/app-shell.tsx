import { WorkspaceSlugProvider } from "@agora/core/paths";
import { useWorkspaceId } from "@agora/core/hooks";
import type { Workspace } from "@agora/core/types";
import { useActiveWorkspace } from "../workspace/use-active-workspace";
import { useDeepLinkWorkspace } from "../workspace/use-deep-link-workspace";
import { useRouter } from "../platform/navigation";
import { CenterMessage } from "./center-message";
import { TabBar } from "./tab-bar";
import { ToastProvider } from "./toast";
import { InboxScreen } from "../screens/inbox-screen";
import { TasksScreen } from "../screens/tasks-screen";
import { CycleScreen } from "../screens/cycle-screen";
import { AgentsScreen } from "../screens/agents-screen";
import { ProfileScreen } from "../screens/profile-screen";
import { ChatSessionScreen } from "../screens/chat-session-screen";
import { IssueDetailScreen } from "../screens/issue-detail-screen";
import { CreateIssueScreen } from "../screens/create-issue-screen";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { useT } from "../i18n";
import { useBackButton } from "../telegram/native-buttons";
import { isTelegramEnv } from "../telegram/sdk";
import { cn } from "../lib/cn";

export function AppShell({
  deepLinkSlug,
  deepLinkIssueId,
}: {
  deepLinkSlug?: string | null;
  deepLinkIssueId?: string | null;
}) {
  // Resolve the issue's real workspace from its UUID (covers legacy links with
  // no slug and last-workspace mismatches), falling back to the link's slug.
  const resolvedSlug = useDeepLinkWorkspace(deepLinkIssueId, deepLinkSlug);
  const { workspaces, active, isLoading, select } = useActiveWorkspace(resolvedSlug);
  const t = useT();

  if (!active) {
    return isLoading ? (
      <CenterMessage spinner title={t("common.loading")} />
    ) : (
      <CenterMessage
        title={t("shell.noWorkspace")}
        subtitle={t("shell.noWorkspaceSub")}
      />
    );
  }

  // Keyed on slug so switching workspace fully remounts the scoped subtree —
  // every query re-keys on the new wsId and stale screen state is dropped.
  return (
    <WorkspaceSlugProvider key={active.slug} slug={active.slug}>
      <ShellInner workspaces={workspaces} active={active} onSelect={select} />
    </WorkspaceSlugProvider>
  );
}

function ShellInner({
  workspaces,
  active,
  onSelect,
}: {
  workspaces: Workspace[];
  active: Workspace;
  onSelect: (slug: string) => void;
}) {
  const wsId = useWorkspaceId();
  const { route, activeTab, back } = useRouter();

  // Telegram's native top-left back button on every non-tab screen.
  useBackButton(route.name !== "tab", back);

  let body: React.ReactNode;
  switch (route.name) {
    case "issue":
      body = <IssueDetailScreen issueId={route.id} />;
      break;
    case "create":
      body = <CreateIssueScreen />;
      break;
    case "chat-session":
      body = <ChatSessionScreen sessionId={route.id} />;
      break;
    case "tab":
    default:
      body =
        activeTab === "tasks" ? (
          <TasksScreen workspaces={workspaces} active={active} onSelect={onSelect} />
        ) : activeTab === "cycle" ? (
          <CycleScreen />
        ) : activeTab === "agents" ? (
          <AgentsScreen />
        ) : activeTab === "profile" ? (
          <ProfileScreen workspace={active} />
        ) : (
          <InboxScreen />
        );
  }

  const isRootTab = route.name === "tab";
  // Inside Telegram the client already draws its own chrome (bot title, close,
  // menu pill) — an in-app brand header on top of that reads as clutter. Keep
  // the lockup only for the browser preview, and give root tabs just the
  // safe-area clearance in Telegram.
  const inTelegram = isTelegramEnv();

  return (
    <ToastProvider>
      <div className="relative flex h-full min-h-0 flex-1 flex-col bg-sidebar dark:bg-background">
        {isRootTab && !inTelegram && (
          <header className="flex shrink-0 items-center justify-center gap-[7px] px-4 pb-1 pt-[max(env(safe-area-inset-top),0.5rem)]">
            <AgoraIcon className="size-5 text-brand" noSpin />
            <span className="text-base font-semibold tracking-tight text-foreground">
              Agora
            </span>
          </header>
        )}

        <main
          className={cn(
            "flex min-h-0 flex-1 flex-col overflow-hidden",
            isRootTab && inTelegram && "pt-[max(env(safe-area-inset-top),0.375rem)]",
          )}
        >
          {body}
        </main>

        {isRootTab && <TabBar wsId={wsId} />}
      </div>
    </ToastProvider>
  );
}
