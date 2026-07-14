import { Bot, CircleCheckBig, Inbox, RefreshCw, User } from "lucide-react";
import { useInboxUnreadCount } from "@agora/core/inbox/queries";
import { useRouter, type Tab } from "../platform/navigation";
import { haptic } from "../telegram/sdk";
import { useT } from "../i18n";
import { cn } from "../lib/cn";

const TABS: { key: Tab; Icon: typeof Inbox }[] = [
  { key: "tasks", Icon: CircleCheckBig },
  { key: "cycle", Icon: RefreshCw },
  { key: "agents", Icon: Bot },
  { key: "inbox", Icon: Inbox },
  { key: "profile", Icon: User },
];

export function TabBar({ wsId }: { wsId: string }) {
  const { activeTab, openTab } = useRouter();
  const unread = useInboxUnreadCount(wsId);
  const t = useT();

  return (
    <nav className="flex shrink-0 items-stretch border-t border-border bg-card px-1 pb-[max(env(safe-area-inset-bottom),0.5rem)] pt-2">
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
              "relative flex flex-1 flex-col items-center gap-1 py-1 text-[10px] font-medium transition-colors",
              active ? "text-brand" : "text-muted-foreground",
            )}
          >
            <span className="relative">
              <Icon className="size-6" />
              {key === "inbox" && unread > 0 && (
                <span className="absolute -right-2.5 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full border-2 border-card bg-brand px-1 text-[9px] font-bold leading-none text-brand-foreground">
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
