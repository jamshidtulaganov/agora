import { Inbox, ListTodo, MessageSquare } from "lucide-react";
import { useInboxUnreadCount } from "@agora/core/inbox/queries";
import { useRouter, type Tab } from "../platform/navigation";
import { haptic } from "../telegram/sdk";
import { useT } from "../i18n";
import { cn } from "../lib/cn";

const TABS: { key: Tab; Icon: typeof Inbox }[] = [
  { key: "inbox", Icon: Inbox },
  { key: "issues", Icon: ListTodo },
  { key: "chat", Icon: MessageSquare },
];

export function TabBar({ wsId }: { wsId: string }) {
  const { activeTab, openTab } = useRouter();
  const unread = useInboxUnreadCount(wsId);
  const t = useT();

  return (
    <nav className="flex shrink-0 items-stretch border-t border-border bg-card pb-[env(safe-area-inset-bottom)]">
      {TABS.map(({ key, Icon }) => {
        const active = activeTab === key;
        return (
          <button
            key={key}
            type="button"
            onClick={() => {
              haptic("light");
              openTab(key);
            }}
            className={cn(
              "relative flex flex-1 flex-col items-center gap-1 py-2 text-[11px] font-medium transition-colors",
              active ? "text-brand" : "text-muted-foreground",
            )}
          >
            <span className="relative">
              <Icon className="size-5" />
              {key === "inbox" && unread > 0 && (
                <span className="absolute -right-2 -top-1.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-destructive px-1 text-[9px] font-semibold text-destructive-foreground">
                  {unread > 99 ? "99+" : unread}
                </span>
              )}
            </span>
            {t(`tab.${key}`)}
          </button>
        );
      })}
    </nav>
  );
}
