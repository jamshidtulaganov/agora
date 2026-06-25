import { Plus } from "lucide-react";
import { WorkspaceSlugProvider } from "@agora/core/paths";
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
import { cn } from "../lib/cn";

export function AppShell() {
  const { workspaces, active, isLoading, select } = useActiveWorkspace();

  if (!active) {
    return isLoading ? (
      <CenterMessage spinner title="Loading…" />
    ) : (
      <CenterMessage
        title="No workspace"
        subtitle="Your account isn’t a member of any workspace yet."
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
  const { route, activeTab, navigate } = useRouter();

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
        activeTab === "inbox" ? (
          <InboxScreen />
        ) : activeTab === "issues" ? (
          <IssuesScreen />
        ) : (
          <ChatScreen />
        );
  }

  const isRootTab = route.name === "tab";

  return (
    <div className="flex h-full min-h-0 flex-1 flex-col">
      {isRootTab && (
        <header className="flex shrink-0 items-center gap-2 border-b border-border bg-card px-3 py-2 pt-[max(env(safe-area-inset-top),0.5rem)]">
          <div className="flex min-w-0 flex-1 items-center gap-1.5 overflow-x-auto">
            {workspaces.map((w) => (
              <button
                key={w.id}
                type="button"
                onClick={() => onSelect(w.slug)}
                className={cn(
                  "shrink-0 rounded-full px-3 py-1 text-xs font-medium transition-colors",
                  w.id === active.id
                    ? "bg-[var(--brand,theme(colors.blue.600))] text-white"
                    : "bg-muted text-muted-foreground",
                )}
              >
                {w.name}
              </button>
            ))}
          </div>
          <button
            type="button"
            onClick={() => navigate({ name: "create" })}
            className="flex shrink-0 items-center gap-1 rounded-full bg-[var(--brand,theme(colors.blue.600))] px-3 py-1.5 text-xs font-semibold text-white"
          >
            <Plus className="size-3.5" />
            New
          </button>
        </header>
      )}

      <main className="flex min-h-0 flex-1 flex-col overflow-hidden">{body}</main>

      {isRootTab && <TabBar wsId={wsId} />}
    </div>
  );
}
