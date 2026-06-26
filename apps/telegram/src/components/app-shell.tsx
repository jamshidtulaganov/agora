import { Plus, Settings } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { resolvePublicFileUrl } from "@agora/core/workspace/avatar-url";
import { WorkspaceSlugProvider } from "@agora/core/paths";
import { agentListOptions } from "@agora/core/workspace/queries";
import { useWorkspaceId } from "@agora/core/hooks";
import type { Workspace } from "@agora/core/types";
import { useActiveWorkspace } from "../workspace/use-active-workspace";
import { useRouter } from "../platform/navigation";
import { CenterMessage } from "./center-message";
import { TabBar } from "./tab-bar";
import { InboxScreen } from "../screens/inbox-screen";
import { IssuesScreen } from "../screens/issues-screen";
import { ChatScreen } from "../screens/chat-screen";
import { ChatSessionScreen } from "../screens/chat-session-screen";
import { IssueDetailScreen } from "../screens/issue-detail-screen";
import { CreateIssueScreen } from "../screens/create-issue-screen";
import { SettingsScreen } from "../screens/settings-screen";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { useT } from "../i18n";
import { useBackButton } from "../telegram/native-buttons";
import { cn } from "../lib/cn";

export function AppShell({ deepLinkSlug }: { deepLinkSlug?: string | null }) {
  const { workspaces, active, isLoading, select } = useActiveWorkspace(deepLinkSlug);
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

// Small round workspace logo for the header chips — image when the workspace
// has an avatar (served same-origin via the SPA proxy), else its initial.
function WorkspaceMark({ workspace }: { workspace: Workspace }) {
  const url = resolvePublicFileUrl(workspace.avatar_url);
  if (url) {
    return <img src={url} alt="" className="size-5 shrink-0 rounded-full object-cover" />;
  }
  return (
    <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-foreground/15 text-[10px] font-bold">
      {workspace.name.charAt(0).toUpperCase()}
    </span>
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
  const { route, activeTab, navigate, back } = useRouter();
  const t = useT();

  // Chat is only useful once the workspace has an AI agent — hide the tab
  // otherwise so it isn't a dead end. Auto-appears when an agent is created.
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const hasChat = agents.some((a) => !a.archived_at);

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
    case "settings":
      body = <SettingsScreen />;
      break;
    case "tab":
    default:
      body =
        activeTab === "issues" ? (
          <IssuesScreen />
        ) : activeTab === "chat" && hasChat ? (
          <ChatScreen />
        ) : (
          <InboxScreen />
        );
  }

  const isRootTab = route.name === "tab";

  return (
    <div className="flex h-full min-h-0 flex-1 flex-col">
      {isRootTab && (
        <header className="flex shrink-0 items-center gap-2 border-b border-border bg-card px-3 py-2 pt-[max(env(safe-area-inset-top),0.5rem)]">
          <div className="flex shrink-0 items-center gap-1.5">
            <AgoraIcon className="size-5 text-foreground" noSpin />
            <span className="text-sm font-semibold tracking-tight text-foreground">Agora</span>
          </div>
          <div className="flex min-w-0 flex-1 items-center gap-1.5 overflow-x-auto">
            {workspaces.map((w) => (
              <button
                key={w.id}
                type="button"
                onClick={() => onSelect(w.slug)}
                className={cn(
                  "flex shrink-0 items-center gap-1.5 rounded-full py-1 pl-1 pr-3 text-xs font-medium transition-colors",
                  w.id === active.id
                    ? "bg-brand text-brand-foreground"
                    : "bg-muted text-muted-foreground",
                )}
              >
                <WorkspaceMark workspace={w} />
                {w.name}
              </button>
            ))}
          </div>
          <button
            type="button"
            onClick={() => navigate({ name: "create" })}
            className="flex shrink-0 items-center gap-1 rounded-full bg-brand px-3 py-1.5 text-xs font-semibold text-brand-foreground"
          >
            <Plus className="size-3.5" />
            {t("shell.new")}
          </button>
          <button
            type="button"
            onClick={() => navigate({ name: "settings" })}
            aria-label={t("settings.title")}
            className="flex shrink-0 items-center justify-center p-1 text-muted-foreground transition-colors active:text-foreground"
          >
            <Settings className="size-5" />
          </button>
        </header>
      )}

      <main className="flex min-h-0 flex-1 flex-col overflow-hidden">{body}</main>

      {isRootTab && <TabBar wsId={wsId} hasChat={hasChat} />}
    </div>
  );
}
